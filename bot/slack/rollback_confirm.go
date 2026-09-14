package slack

import (
	"errors"
	"fmt"
	"strings"

	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	log "github.com/sirupsen/logrus"
)

// interactionContext - where an interaction happened, to answer the user who acted there
type interactionContext struct {
	channelID   string
	threadTS    string
	responseURL string
}

func newInteractionContext(callback slack.InteractionCallback) interactionContext {
	channel := callback.Container.ChannelID
	if channel == "" {
		channel = callback.Channel.ID
	}
	return interactionContext{
		channelID:   channel,
		threadTS:    callback.Container.ThreadTs,
		responseURL: callback.ResponseURL,
	}
}

// interactionResponder answers interactions with messages that only the user who acted sees
type interactionResponder interface {
	PostEphemeral(interaction interactionContext, user string, blocks slack.Blocks, text string) error
	ReplaceEphemeral(interaction interactionContext, text string) error
	DeleteEphemeral(interaction interactionContext) error
}

// socketResponder - the interactionResponder of the Slack client
type socketResponder struct {
	client *socketmode.Client
}

func (r socketResponder) PostEphemeral(interaction interactionContext, user string, blocks slack.Blocks, text string) error {
	options := []slack.MsgOption{
		slack.MsgOptionBlocks(blocks.BlockSet...),
		slack.MsgOptionText(text, false),
	}
	if interaction.threadTS != "" {
		options = append(options, slack.MsgOptionTS(interaction.threadTS))
	}
	_, err := r.client.PostEphemeral(interaction.channelID, user, options...)
	return err
}

func (r socketResponder) ReplaceEphemeral(interaction interactionContext, text string) error {
	if interaction.responseURL == "" {
		return errors.New("the interaction has no response url")
	}
	_, _, _, err := r.client.SendMessage(interaction.channelID,
		slack.MsgOptionReplaceOriginal(interaction.responseURL),
		slack.MsgOptionText(text, false),
	)
	return err
}

func (r socketResponder) DeleteEphemeral(interaction interactionContext) error {
	if interaction.responseURL == "" {
		return errors.New("the interaction has no response url")
	}
	_, _, _, err := r.client.SendMessage(interaction.channelID, slack.MsgOptionDeleteOriginal(interaction.responseURL))
	return err
}

func (b *Bot) interactionResponder() interactionResponder {
	if b.interactions != nil {
		return b.interactions
	}
	return socketResponder{client: b.slackSocket}
}

// approvalLookup finds approvals by id, implemented by the approvals manager
type approvalLookup interface {
	GetByID(id string) (*types.Approval, error)
}

func (b *Bot) lookupApproval(id string) (*types.Approval, error) {
	lookup, ok := b.approvalMessages.(approvalLookup)
	if !ok {
		return nil, errors.New("approvals are not available to the bot")
	}
	return lookup.GetByID(id)
}

// handleRollbackAction handles the two steps of a rollback from Slack: Roll back… asks the user who acted to confirm
// in a message only they see, and only the Roll back button of that message requests the rollback. It reports
// whether the command was one of these steps.
func (b *Bot) handleRollbackAction(user, command string, interaction interactionContext) bool {
	action, argument, _ := strings.Cut(command, " ")
	fields := strings.Fields(argument)

	switch action {
	case rollbackRequestActionID:
		if len(fields) != 1 {
			return true
		}
		b.requestRollbackConfirmation(user, fields[0], interaction)
	case rollbackConfirmActionID:
		if len(fields) != 2 {
			b.refuseConfirmedRollback(interaction, "the confirmation is incomplete")
			return true
		}
		b.confirmRollback(user, fields[0], fields[1], interaction)
	case rollbackCancelActionID:
		if err := b.interactionResponder().DeleteEphemeral(interaction); err != nil {
			log.WithFields(log.Fields{"error": err}).Debug("bot.slack: failed to remove the rollback confirmation")
		}
	default:
		return false
	}
	return true
}

// requestRollbackConfirmation asks the user to confirm the rollback, or explains why the approval can not be rolled
// back
func (b *Bot) requestRollbackConfirmation(user, approvalID string, interaction interactionContext) {
	responder := b.interactionResponder()

	blocks, text := rollbackRefusal("the approval was not found")
	if approval, err := b.lookupApproval(approvalID); err == nil {
		if err := approval.RollbackError(); err != nil {
			blocks, text = rollbackRefusal(err.Error())
		} else {
			blocks, text = createRollbackConfirmation(approval)
		}
	}

	if err := responder.PostEphemeral(interaction, user, blocks, text); err != nil {
		log.WithFields(log.Fields{
			"error":       err,
			"user":        user,
			"approval_id": approvalID,
		}).Error("bot.slack: failed to ask for the rollback confirmation")
	}
}

// confirmRollback requests the rollback the user confirmed, unless the approval changed since the confirmation was
// shown, and replaces the confirmation with what happened
func (b *Bot) confirmRollback(user, approvalID, fingerprint string, interaction interactionContext) {
	approval, err := b.lookupApproval(approvalID)
	switch {
	case err != nil:
		b.refuseConfirmedRollback(interaction, "the approval was not found")
		return
	case approval.RollbackError() != nil:
		b.refuseConfirmedRollback(interaction, approval.RollbackError().Error())
		return
	case approval.RollbackFingerprint() != fingerprint:
		b.refuseConfirmedRollback(interaction, types.ErrRollbackChanged.Error())
		return
	}

	// the same path as approvals, the approvals manager checks the fingerprint again
	b.approvalsRespCh <- &bot.ApprovalResponse{
		User:     user,
		Text:     fmt.Sprintf("%s %s %s", bot.RollbackResponseKeyword, approval.ID, fingerprint),
		Rollback: true,
	}

	_, group := approvalWorkload(approval)
	text := fmt.Sprintf(":rewind: Rolling back %s to %s…", escapeMrkdwn(rolloutNames(approval, group)), escapeMrkdwn(previousReference(approval)))
	if err := b.interactionResponder().ReplaceEphemeral(interaction, text); err != nil {
		log.WithFields(log.Fields{"error": err}).Debug("bot.slack: failed to replace the rollback confirmation")
	}
}

func (b *Bot) refuseConfirmedRollback(interaction interactionContext, reason string) {
	_, text := rollbackRefusal(reason)
	if err := b.interactionResponder().ReplaceEphemeral(interaction, text); err != nil {
		log.WithFields(log.Fields{"error": err}).Debug("bot.slack: failed to explain the refused rollback")
	}
}

// createRollbackConfirmation - the message asking to confirm a rollback, with the Roll back button carrying the
// approval and the rollback fingerprint it was shown for
func createRollbackConfirmation(req *types.Approval) (slack.Blocks, string) {
	_, group := approvalWorkload(req)
	var names []string
	for _, target := range req.Rollout {
		names = append(names, shortMemberName(target.Name, group))
	}
	text := rollbackConfirmationText(req, names)

	confirm := slack.NewButtonBlockElement(
		rollbackConfirmActionID,
		req.ID+" "+req.RollbackFingerprint(),
		slack.NewTextBlockObject("plain_text", "Roll back", true, false),
	)
	confirm.Style = slack.StyleDanger
	cancel := slack.NewButtonBlockElement(
		rollbackCancelActionID,
		req.ID,
		slack.NewTextBlockObject("plain_text", "Cancel", true, false),
	)

	return slack.Blocks{BlockSet: []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil),
		slack.NewActionBlock("", confirm, cancel),
	}}, text
}

// rollbackRefusal - the message explaining why a rollback was not requested
func rollbackRefusal(reason string) (slack.Blocks, string) {
	text := ":no_entry_sign: Not rolled back: " + escapeMrkdwn(reason)
	return slack.Blocks{BlockSet: []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil),
	}}, text
}

// rolloutNames - the names of the rolled out resources, without the group name prefix
func rolloutNames(req *types.Approval, group string) string {
	var names []string
	for _, target := range req.Rollout {
		names = append(names, shortMemberName(target.Name, group))
	}
	return strings.Join(names, ", ")
}
