package slack

import (
	"fmt"
	"github.com/keel-hq/keel/bot"
	log "github.com/sirupsen/logrus"
	"strings"
	"unicode"

	"github.com/keel-hq/keel/types"
	"github.com/keel-hq/keel/util/scm"
	"github.com/slack-go/slack"
)

// Request - request approval
func (b *Bot) RequestApproval(req *types.Approval) error {
	blocks, text := b.createApprovalMessage("Approval required! :mega:", req)
	return b.postApprovalMessageBlock(req.ID, blocks, text)
}

func (b *Bot) ReplyToApproval(approval *types.Approval) error {
	var title string
	switch approval.Status() {
	case types.ApprovalStatusPending:
		title = "Approval required! :mega:"
	case types.ApprovalStatusRejected:
		title = "Change rejected! :negative_squared_cross_mark:"
	case types.ApprovalStatusApproved:
		title = "Change approved! :tada:"
	}

	blocks, text := b.createApprovalMessage(title, approval)
	b.upsertApprovalMessage(approval.ID, blocks, text)
	return nil
}

// createApprovalMessage - build the approval message in the configured layout, along with its notification
// text (empty for the default layout, which has none)
func (b *Bot) createApprovalMessage(title string, req *types.Approval) (slack.Blocks, string) {
	if b.compactApprovals {
		return createCompactBlockMessage(req)
	}
	return createBlockMessage(title, b.name, !b.hideApprovalCommands, req), ""
}

func createBlockMessage(title string, botName string, showCommands bool, req *types.Approval) slack.Blocks {
	if req.Expired() {
		title = title + " (Expired)"
	}

	headerText := slack.NewTextBlockObject(
		"plain_text",
		title,
		true,
		false,
	)
	headerSection := slack.NewHeaderBlock(headerText)

	messageSection := slack.NewTextBlockObject(
		"mrkdwn",
		req.Message,
		false,
		false,
	)
	messageBlock := slack.NewSectionBlock(messageSection, nil, nil)

	votesField := slack.NewTextBlockObject(
		"mrkdwn",
		fmt.Sprintf("*Votes:*\n%d/%d", req.VotesReceived, req.VotesRequired),
		false,
		false,
	)
	deltaField := slack.NewTextBlockObject(
		"mrkdwn",
		"*Delta:*\n"+req.Delta(),
		false,
		false,
	)
	leftDetailSection := slack.NewSectionBlock(
		nil,
		[]*slack.TextBlockObject{
			votesField,
			deltaField,
		},
		nil,
	)

	identifierField := slack.NewTextBlockObject(
		"mrkdwn",
		"*Identifier:*\n"+req.Identifier,
		false,
		false,
	)
	providerField := slack.NewTextBlockObject(
		"mrkdwn",
		"*Provider:*\n"+req.Provider.String(),
		false,
		false,
	)
	rightDetailSection := slack.NewSectionBlock(nil, []*slack.TextBlockObject{identifierField, providerField}, nil)

	blocks := []slack.Block{
		headerSection,
		messageBlock,
		leftDetailSection,
		rightDetailSection,
	}

	if changeFields := createChangeFields(req); len(changeFields) > 0 {
		blocks = append(blocks, slack.NewSectionBlock(nil, changeFields, nil))
	}

	if showCommands {
		blocks = append(blocks, createCommandBlocks(botName)...)
	}

	if req.VotesReceived < req.VotesRequired && !req.Expired() && !req.Rejected {
		blocks = append(
			blocks,
			slack.NewDividerBlock(),
			createApprovalButtons(req.Identifier),
		)
	}
	return slack.Blocks{
		BlockSet: blocks,
	}
}

// createApprovalButtons - the approve and reject buttons of an approval request
func createApprovalButtons(identifier string) *slack.ActionBlock {
	approveButton := slack.NewButtonBlockElement(
		bot.ApprovalResponseKeyword,
		identifier,
		slack.NewTextBlockObject(
			"plain_text",
			"Approve",
			true,
			false,
		),
	)
	approveButton.Style = slack.StylePrimary

	rejectButton := slack.NewButtonBlockElement(
		bot.RejectResponseKeyword,
		identifier,
		slack.NewTextBlockObject(
			"plain_text",
			"Reject",
			true,
			false,
		),
	)
	rejectButton.Style = slack.StyleDanger

	return slack.NewActionBlock("", approveButton, rejectButton)
}

// createCommandBlocks - list the supported bot commands, as returned by the help command
func createCommandBlocks(botName string) []slack.Block {
	commands := bot.BotEventTextToResponse["help"]
	var commandTexts []slack.MixedElement

	for i, cmd := range commands {
		// -- avoid adding first line in commands which is the title.
		if i == 0 {
			continue
		}
		cmd = addBotMentionToCommand(cmd, botName)
		commandTexts = append(commandTexts, slack.NewTextBlockObject("mrkdwn", cmd, false, false))
	}
	commandsBlock := slack.NewContextBlock("", commandTexts...)
	header := commands[0]

	return []slack.Block{
		slack.NewDividerBlock(),
		slack.NewContextBlock("", slack.NewTextBlockObject("mrkdwn", header, false, false)),
		commandsBlock,
	}
}

// createChangeFields - describe the code change behind the new image when its revision is known: a link to
// the commit and, when the running revision is known too, a link to the changes in between. Revisions hosted
// on services that are not recognized are shown as text.
func createChangeFields(req *types.Approval) []*slack.TextBlockObject {
	if req.NewRevision == "" {
		return nil
	}

	links := scm.ChangeLinks(req.SourceURL, req.CurrentRevision, req.NewRevision)

	commit := "`" + escapeMrkdwn(req.NewRevision) + "`"
	if links.Commit != "" {
		commit = fmt.Sprintf("<%s|%s>", links.Commit, escapeMrkdwn(shortRevision(req.NewRevision)))
	}
	fields := []*slack.TextBlockObject{
		slack.NewTextBlockObject("mrkdwn", "*Commit:*\n"+commit, false, false),
	}

	if links.Compare != "" {
		changes := fmt.Sprintf("<%s|%s...%s>",
			links.Compare,
			escapeMrkdwn(shortRevision(req.CurrentRevision)),
			escapeMrkdwn(shortRevision(req.NewRevision)),
		)
		fields = append(fields, slack.NewTextBlockObject("mrkdwn", "*Changes:*\n"+changes, false, false))
	}

	return fields
}

// shortRevision - abbreviate a full git commit hash to 7 characters, other revisions are kept as they are
func shortRevision(revision string) string {
	if len(revision) != 40 && len(revision) != 64 {
		return revision
	}
	for _, r := range revision {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return revision
		}
	}
	return revision[:7]
}

// escapeMrkdwn - escape the characters that Slack uses for its control sequences
func escapeMrkdwn(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

func addBotMentionToCommand(command string, botName string) string {
	// -- retrieve the first letter of the command in order to insert bot mention
	firstLetterPos := -1
	for i, r := range command {
		if unicode.IsLetter(r) {
			firstLetterPos = i
			break
		}
	}

	if firstLetterPos < 0 {
		log.Debugf("Unable to find the first letter of the command '%s', let the command without the bot mention.", command)
		return command
	}

	return strings.Replace(
		command[:firstLetterPos]+fmt.Sprintf("@%s ", botName)+command[firstLetterPos:],
		"\"",
		"`",
		-1,
	)
}
