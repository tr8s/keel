package slack

import (
	"fmt"
	"strings"
	"time"

	"github.com/keel-hq/keel/approvals"
	"github.com/keel-hq/keel/types"
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

// createRolloutFailureMessage - the thread reply about a failed rollout: the approvers and what failed
func createRolloutFailureMessage(req *types.Approval) (slack.Blocks, string) {
	_, name := approvalWorkload(req)

	text := rolloutStatus(req, name)
	if voters := req.GetVoters(); len(voters) > 0 {
		text = formatVoters(voters) + " " + text
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil),
	}

	return slack.Blocks{BlockSet: blocks}, "Rollout of " + name + " failed"
}

// rolloutStatus - the line about the rollout of an approved update, empty when nothing rolls out:
//
//	:hourglass_flowing_sand: Rolling out · api 2/2 · portal 1/2
//	:white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>
//	:x: portal not ready after 10m · ImagePullBackOff
func rolloutStatus(req *types.Approval, group string) string {
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
		if voters := req.GetVoters(); len(voters) > 0 {
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
