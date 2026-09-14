package slack

import (
	"fmt"
	"strings"

	"github.com/keel-hq/keel/types"
	"github.com/keel-hq/keel/util/scm"
)

// commitLines - the commits of a change of more than one commit, newest first, then how many more there are:
//
//   - <commit link|896a024> CI retries registry calls
//     and 3 more
//
// Empty for a change of a single commit or without a commit list, which show the commit subject instead.
func commitLines(req *types.Approval) string {
	if req.ChangeCount <= 1 || len(req.ChangeCommits) == 0 {
		return ""
	}

	lines := make([]string, 0, len(req.ChangeCommits)+1)
	for _, commit := range req.ChangeCommits {
		sha := escapeMrkdwn(shortRevision(commit.SHA))
		if link := scm.ChangeLinks(req.SourceURL, "", commit.SHA).Commit; link != "" {
			sha = fmt.Sprintf("<%s|%s>", link, sha)
		}
		lines = append(lines, fmt.Sprintf("• %s %s", sha, escapeMrkdwn(truncateText(commit.Subject, maxCommitSubjectLength))))
	}
	if more := req.ChangeCount - len(req.ChangeCommits); more > 0 {
		lines = append(lines, fmt.Sprintf("and %d more", more))
	}

	return strings.Join(lines, "\n")
}

// commitHeadline - the notification headline of a change: for a change of more than one commit its newest commit
// subject and the number of other commits, ie: "CI retries registry calls (+2)", otherwise the commit subject
func commitHeadline(req *types.Approval, subject string) string {
	if req.ChangeCount <= 1 || len(req.ChangeCommits) == 0 {
		return subject
	}
	return fmt.Sprintf("%s (+%d)", truncateText(req.ChangeCommits[0].Subject, maxCommitSubjectLength), req.ChangeCount-1)
}
