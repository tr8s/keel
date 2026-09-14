package slack

import (
	"fmt"
	"strings"

	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"
)

// defaultNoticeMigrationNote - what a deploy notice says about a database migration when no note is configured
const defaultNoticeMigrationNote = "Keel does not run migrations."

// createDeployNoticeMessage - the deploy notice of an update that needed no approval, in the compact approval layout
// without votes or interactive elements: the workload or group with its commit, the commit
// list or subject, the migration warning, a context line with the namespace, the members, the author and a link to
// the changes, and the rollout status. It also returns the notification text of the message.
func createDeployNoticeMessage(req *types.Approval, migrationNote string) (slack.Blocks, string) {
	namespace, name, members := noticeWorkload(req)

	blocks := compactChangeBlocks(req, name, migrationNote)

	details := compactDetails(req, namespace, members)
	if changes := changesURL(req); changes != "" {
		details = append(details, fmt.Sprintf("<%s|View changes>", changes))
	}
	if len(details) > 0 {
		blocks = append(blocks, compactContext(strings.Join(details, " · ")))
	}
	blocks = append(blocks, compactContext(noticeStatus(req, name)))

	return slack.Blocks{BlockSet: blocks}, compactText("Deploying", req, name, members)
}

// noticeStatus - the status line of a deploy notice, ie:
//
//	:hourglass_flowing_sand: Rolling out · api 1/2 · portal 2/2
//	:white_check_mark: Live on api, portal in 30s
//	:x: portal not ready after 10m · ImagePullBackOff
//	:fast_forward: Superseded by 9c1d2e3
func noticeStatus(req *types.Approval, group string) string {
	if req.SupersededBy != "" {
		return ":fast_forward: Superseded by " + escapeMrkdwn(shortChange(req.SupersededBy))
	}
	if status := rolloutStatus(req, group, false); status != "" {
		return status
	}
	return ":hourglass_flowing_sand: Rolling out"
}

// noticeFailureText - the thread reply about a failed rollout of a deploy notice, after the mention when there is one
func noticeFailureText(req *types.Approval, mention string) string {
	_, name, _ := noticeWorkload(req)
	text := rolloutStatus(req, name, false)
	if mention != "" {
		text = mention + " " + text
	}
	return text
}

// noticeWorkload - namespace and name of what a deploy notice is for and the short names of its members: the approval
// group and its members for a grouped notice, otherwise the resource of the notice and no members
// ie: notice/deployment/selfled/selfled-api:4c80a47... -> selfled, selfled-api
func noticeWorkload(req *types.Approval) (namespace, name string, members []string) {
	if req.Group != "" {
		namespace, name = approvalWorkload(req)
		return namespace, name, memberNames(req, name)
	}
	parts := strings.Split(types.NoticeKey(req.Identifier), "/")
	name = parts[len(parts)-1]
	if len(parts) > 2 {
		namespace = parts[len(parts)-2]
	}
	return namespace, name, nil
}

// noticeFinished - whether a deploy notice will not change any more unless a member joins it: it was superseded, or
// its rollout is live or failed
func noticeFinished(req *types.Approval) bool {
	state := req.RolloutState()
	return req.SupersededBy != "" || state == types.RolloutStateLive || state == types.RolloutStateFailed
}
