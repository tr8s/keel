package slack

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/keel-hq/keel/types"
	"github.com/keel-hq/keel/util/scm"
	"github.com/slack-go/slack"
)

// maxCommitSubjectLength - longest commit subject shown, longer subjects are cut
const maxCommitSubjectLength = 200

// slackUserID - Slack user identifiers, which the Slack bot records as voters
var slackUserID = regexp.MustCompile(`^[UW][A-Z0-9]{6,}$`)

// createCompactBlockMessage - build a compact approval message focused on the change being deployed: the
// workload and its commit, the commit subject, and a context line with the namespace, the author, a link to
// the changes and the votes when more than one is required. Pending approvals get the approve and reject
// buttons, decided or expired ones their outcome. It also returns the notification text of the message.
func createCompactBlockMessage(req *types.Approval) (slack.Blocks, string) {
	namespace, name := approvalWorkload(req)
	members := memberNames(req, name)
	reference := shortChangeReference(req)
	links := scm.ChangeLinks(req.SourceURL, req.CurrentRevision, req.NewRevision)

	change := "`" + escapeMrkdwn(reference) + "`"
	if links.Commit != "" {
		change = fmt.Sprintf("<%s|%s>", links.Commit, escapeMrkdwn(reference))
	}
	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*%s* → %s", escapeMrkdwn(name), change), false, false),
			nil,
			nil,
		),
	}

	subject := truncateText(req.CommitSubject, maxCommitSubjectLength)
	if subject != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", escapeMrkdwn(subject), false, false),
			nil,
			nil,
		))
	}

	var details []string
	if namespace != "" {
		details = append(details, escapeMrkdwn(namespace))
	}
	if len(members) > 0 {
		details = append(details, escapeMrkdwn(strings.Join(members, ", ")))
	}
	if req.CommitAuthor != "" {
		details = append(details, escapeMrkdwn(req.CommitAuthor))
	}
	if links.Compare != "" {
		details = append(details, fmt.Sprintf("<%s|%s…%s>",
			links.Compare,
			escapeMrkdwn(shortRevision(req.CurrentRevision)),
			escapeMrkdwn(shortRevision(req.NewRevision)),
		))
	}
	if req.VotesRequired > 1 {
		details = append(details, fmt.Sprintf("%d/%d votes", req.VotesReceived, req.VotesRequired))
	}
	if len(details) > 0 {
		blocks = append(blocks, compactContext(strings.Join(details, " · ")))
	}

	switch {
	case req.SupersededBy != "":
		blocks = append(blocks, compactContext(":fast_forward: Superseded by "+escapeMrkdwn(shortChange(req.SupersededBy))))
	case req.Rejected:
		blocks = append(blocks, compactContext(":x: Rejected"))
	case req.VotesReceived >= req.VotesRequired:
		if status := rolloutStatus(req, name); status != "" {
			blocks = append(blocks, compactContext(status))
			break
		}
		outcome := ":white_check_mark: Approved"
		if voters := req.GetVoters(); len(voters) > 0 {
			outcome += " by " + formatVoters(voters)
		}
		blocks = append(blocks, compactContext(outcome))
	case req.Expired():
		blocks = append(blocks, compactContext(":hourglass: Expired"))
	default:
		blocks = append(blocks, createApprovalButtons(req.Identifier))
	}

	text := "Deploy " + name
	if len(members) > 0 {
		text += " (" + strings.Join(members, ", ") + ")"
	}
	text += " " + reference
	if subject != "" {
		text += ": " + subject
	}

	return slack.Blocks{BlockSet: blocks}, text
}

func compactContext(text string) *slack.ContextBlock {
	return slack.NewContextBlock("", slack.NewTextBlockObject("mrkdwn", text, false, false))
}

// memberNames - names of the members of a group approval, without the group name prefix
// ie: trackeid-api in group trackeid -> api
func memberNames(req *types.Approval, group string) []string {
	names := make([]string, 0, len(req.Members))
	for _, member := range req.Members {
		names = append(names, shortMemberName(member.Name, group))
	}
	return names
}

// shortChange - abbreviate a revision or a digest, other references are kept as they are
func shortChange(change string) string {
	if strings.Contains(change, ":") {
		return types.ShortDigest(change)
	}
	return shortRevision(change)
}

// approvalWorkload - namespace and name of the workload an approval is for: the approval group of a group
// approval, otherwise the resource in its identifier
// ie: deployment/trackeid/trackeid-portal:main -> trackeid, trackeid-portal
func approvalWorkload(req *types.Approval) (namespace, name string) {
	if groupNamespace, group, ok := strings.Cut(req.Group, "/"); ok {
		return groupNamespace, group
	}
	parts := strings.Split(strings.TrimSuffix(req.Identifier, ":"+req.NewVersion), "/")
	name = parts[len(parts)-1]
	if len(parts) > 1 {
		namespace = parts[len(parts)-2]
	}
	return namespace, name
}

// shortChangeReference - identify the new image briefly: its short commit when the revision is known,
// otherwise its short digest or, when that is unknown too, its version
func shortChangeReference(req *types.Approval) string {
	switch {
	case req.NewRevision != "":
		return shortRevision(req.NewRevision)
	case req.NewDigest != "":
		return types.ShortDigest(req.NewDigest)
	default:
		return req.NewVersion
	}
}

// formatVoters - list voters in a stable order, mentioning Slack users and naming the others
func formatVoters(voters []string) string {
	sort.Strings(voters)
	formatted := make([]string, 0, len(voters))
	for _, voter := range voters {
		if slackUserID.MatchString(voter) {
			formatted = append(formatted, "<@"+voter+">")
		} else {
			formatted = append(formatted, escapeMrkdwn(voter))
		}
	}
	return strings.Join(formatted, ", ")
}

// truncateText - trim the text and cut it to at most max characters
func truncateText(text string, max int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return string([]rune(text)[:max-1]) + "…"
}
