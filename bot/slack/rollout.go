package slack

import (
	"fmt"
	"strings"
	"time"

	"github.com/keel-hq/keel/approvals"
	"github.com/keel-hq/keel/types"
	"github.com/keel-hq/keel/util/scm"
	"github.com/slack-go/slack"

	log "github.com/sirupsen/logrus"
)

// approvalMessageRecorder records where approval messages were posted
type approvalMessageRecorder interface {
	SetApprovalMessage(id, channel, timestamp string) error
}

// SetApprovalsManager - record the approval messages the bot posts, so that it can update them without searching
// the channel history and reply in their threads
func (b *Bot) SetApprovalsManager(manager approvals.Manager) {
	b.approvalMessages = manager
}

func (b *Bot) recordApprovalMessage(approvalID, channel, timestamp string) {
	if b.approvalMessages == nil || approvalID == "" || channel == "" || timestamp == "" {
		return
	}
	if err := b.approvalMessages.SetApprovalMessage(approvalID, channel, timestamp); err != nil {
		log.WithFields(log.Fields{
			"error":       err,
			"approval_id": approvalID,
		}).Debug("bot.slack: failed to record the approval message")
	}
}

// NotifyRolloutFailure - reply in the thread of the approval message when an approved update failed to roll out,
// mentioning the approvers
func (b *Bot) NotifyRolloutFailure(approval *types.Approval) error {
	if approval.MessageChannel == "" || approval.MessageTimestamp == "" {
		return fmt.Errorf("no message recorded for approval %s", approval.Identifier)
	}

	blocks, text := createRolloutFailureMessage(approval)
	_, _, err := b.slackSocket.PostMessage(
		approval.MessageChannel,
		approvalMessageOptions(text,
			slack.MsgOptionTS(approval.MessageTimestamp),
			slack.MsgOptionBlocks(blocks.BlockSet...),
		)...,
	)
	return err
}

// createRolloutFailureMessage - the thread reply about a failed rollout: the approvers, what failed, and a roll
// back button
func createRolloutFailureMessage(req *types.Approval) (slack.Blocks, string) {
	_, name := approvalWorkload(req)

	text := rolloutStatus(req, name, false)
	if voters := req.GetVoters(); len(voters) > 0 {
		text = formatVoters(voters) + " " + text
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil),
	}
	if req.RollbackError() == nil {
		blocks = append(blocks, createRollbackButton(req, "Roll back"))
	}

	return slack.Blocks{BlockSet: blocks}, "Rollout of " + name + " failed"
}

// rolloutBlocks - the blocks about the rollout of an approved update: its progress or outcome and a roll back
// button once it is live or failed, or the rollback and its own rollout once rolled back. Nil when nothing rolled
// out.
func rolloutBlocks(req *types.Approval, group string) []slack.Block {
	if req.RolledBackBy != "" {
		blocks := []slack.Block{
			compactContext(fmt.Sprintf(":rewind: Rolled back to %s by %s · database changes are not rolled back",
				escapeMrkdwn(previousReference(req)),
				formatVoters([]string{req.RolledBackBy}),
			)),
		}
		if status := rolloutStatus(req, group, false); status != "" {
			blocks = append(blocks, compactContext(status))
		}
		return blocks
	}

	status := rolloutStatus(req, group, true)
	if status == "" {
		return nil
	}

	if state := req.RolloutState(); (state == types.RolloutStateLive || state == types.RolloutStateFailed) && req.RollbackError() == nil {
		// rolling back sits in the overflow menu of the rollout line, so it is not clicked by accident. The menu
		// needs a second option, View changes, and without a link to the changes the button is shown instead.
		if changes := changesURL(req); changes != "" {
			return []slack.Block{slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", status, false, false),
				nil,
				slack.NewAccessory(createApprovalMenu(req, changes)),
			)}
		}
		return []slack.Block{compactContext(status), createRollbackButton(req, "Roll back…")}
	}

	blocks := []slack.Block{compactContext(status)}
	if req.RollbackFailure != "" {
		blocks = append(blocks, compactContext(":warning: Roll back failed: "+escapeMrkdwn(req.RollbackFailure)))
	}
	return blocks
}

// changesURL - the link to the changes of an approval: the comparison of the running and the new revision, or else
// the new commit. Empty when the source host is unknown or the revisions are not.
func changesURL(req *types.Approval) string {
	links := scm.ChangeLinks(req.SourceURL, req.CurrentRevision, req.NewRevision)
	if links.Compare != "" {
		return links.Compare
	}
	return links.Commit
}

// createApprovalMenu - the overflow menu of a live or failed rollout: View changes opens the link to the changes,
// Roll back… asks the user who selected it to confirm. Each option carries its whole command, a link carries none.
// The menu has no Slack confirmation, which would apply to every option.
func createApprovalMenu(req *types.Approval, changes string) *slack.OverflowBlockElement {
	view := slack.NewOptionBlockObject(viewChangesValue, slack.NewTextBlockObject("plain_text", "View changes", false, false), nil)
	view.URL = changes
	rollback := slack.NewOptionBlockObject(
		rollbackRequestActionID+" "+req.ID,
		slack.NewTextBlockObject("plain_text", "Roll back…", false, false),
		nil,
	)

	return slack.NewOverflowBlockElement(approvalMenuActionID, view, rollback)
}

// createRollbackButton - a roll back button that asks the user who clicked it to confirm, ie: in the failure reply
// where speed matters, or instead of the menu when there is no link to the changes
func createRollbackButton(req *types.Approval, label string) *slack.ActionBlock {
	button := slack.NewButtonBlockElement(
		rollbackRequestActionID,
		req.ID,
		slack.NewTextBlockObject("plain_text", label, true, false),
	)
	button.Style = slack.StyleDanger

	return slack.NewActionBlock("", button)
}

// maxConfirmTextLength - the longest rollback confirmation text, which keeps it short enough to read at a glance
const maxConfirmTextLength = 300

// rollbackConfirmationText - what a rollback sets back and that database changes are not rolled back, naming as many
// workloads as fit, ie: "Roll back api, portal and 3 more to 96df4af? ..."
func rollbackConfirmationText(req *types.Approval, names []string) string {
	var text string
	for shown := len(names); shown >= 0; shown-- {
		list := strings.Join(names[:shown], ", ")
		if more := len(names) - shown; more > 0 {
			if shown == 0 {
				list = fmt.Sprintf("%d workloads", more)
			} else {
				list += fmt.Sprintf(" and %d more", more)
			}
		}

		text = fmt.Sprintf("Roll back %s to %s? Database changes are not rolled back.",
			escapeMrkdwn(list),
			escapeMrkdwn(previousReference(req)),
		)
		if req.IncludesMigration {
			text += " This change included a database migration; rolling back the app does not undo it."
		}
		if len([]rune(text)) <= maxConfirmTextLength {
			break
		}
	}
	return text
}

// previousReference - what a rollback sets the resources back to: the revision of the image that ran before when
// it is known, otherwise its digest
func previousReference(req *types.Approval) string {
	if req.CurrentRevision != "" {
		return shortRevision(req.CurrentRevision)
	}
	for _, target := range req.Rollout {
		for _, container := range target.Containers {
			if container.PreviousDigest != "" {
				return types.ShortDigest(container.PreviousDigest)
			}
		}
	}
	return "the previous image"
}

// rolloutStatus - the line about the rollout of an approved update or of its rollback, empty when nothing rolls
// out. The approvers are named on the live line when approvers is set:
//
//	:hourglass_flowing_sand: Rolling out · api 2/2 · portal 1/2
//	:white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>
//	:x: portal not ready after 10m · ImagePullBackOff
func rolloutStatus(req *types.Approval, group string, approvers bool) string {
	switch req.RolloutState() {
	case types.RolloutStateRolling:
		parts := []string{":hourglass_flowing_sand: Rolling out"}
		for _, target := range req.Rollout {
			if target.State == types.RolloutStateReplaced {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %d/%d", escapeMrkdwn(shortMemberName(target.Name, group)), target.Ready, target.Desired))
		}
		return strings.Join(parts, " · ")

	case types.RolloutStateLive:
		var names []string
		for _, target := range req.Rollout {
			if target.State == types.RolloutStateLive {
				names = append(names, shortMemberName(target.Name, group))
			}
		}
		status := ":white_check_mark: Live on " + escapeMrkdwn(strings.Join(names, ", ")) + " in " + formatDuration(req.RolloutDuration())
		if voters := req.GetVoters(); approvers && len(voters) > 0 {
			status += " · approved by " + formatVoters(voters)
		}
		return status

	case types.RolloutStateFailed:
		var failures []string
		for _, target := range req.Rollout {
			if target.State != types.RolloutStateFailed {
				continue
			}
			failures = append(failures, fmt.Sprintf("%s not ready after %s · %s",
				escapeMrkdwn(shortMemberName(target.Name, group)),
				formatDuration(target.FinishedAt.Sub(target.StartedAt)),
				escapeMrkdwn(target.Reason),
			))
		}
		return ":x: " + strings.Join(failures, " · ")
	}

	return ""
}

// shortMemberName - the name of a workload without the group name prefix, ie: trackeid-api in group trackeid -> api
func shortMemberName(name, group string) string {
	if short := strings.TrimPrefix(name, group+"-"); short != "" {
		return short
	}
	return name
}

// formatDuration - a duration rounded to the second without zero units, ie: 41s, 10m, 1m30s
func formatDuration(d time.Duration) string {
	formatted := d.Round(time.Second).String()
	if strings.HasSuffix(formatted, "m0s") {
		formatted = strings.TrimSuffix(formatted, "0s")
	}
	if strings.HasSuffix(formatted, "h0m") {
		formatted = strings.TrimSuffix(formatted, "0m")
	}
	return formatted
}
