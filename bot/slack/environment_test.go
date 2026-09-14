package slack

import (
	"strings"
	"testing"

	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"
)

func TestEnvironmentLabelInCompactHeader(t *testing.T) {
	const (
		approvalCommit = "<https://github.com/tr8s/trackeid/commit/8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910|8f714c1>"
		noticeCommit   = "<https://github.com/tr8s/trackeid/commit/" + noticeRevision + "|8b4d196>"
	)

	tests := []struct {
		name        string
		environment string
		render      func(environment string) (slack.Blocks, string)
		header      string
		text        string
	}{
		{
			name: "approval without environment",
			render: func(environment string) (slack.Blocks, string) {
				req := groupApproval()
				req.Environment = environment
				return createCompactBlockMessage(req)
			},
			header: `"text":"*trackeid* → ` + approvalCommit + `"`,
			text:   "Deploy trackeid (api, portal) 8f714c1: Label images with their commit and source",
		},
		{
			name:        "approval with environment",
			environment: "prod",
			render: func(environment string) (slack.Blocks, string) {
				req := groupApproval()
				req.Environment = environment
				return createCompactBlockMessage(req)
			},
			header: `"text":"*trackeid · prod* → ` + approvalCommit + `"`,
			text:   "Deploy trackeid · prod (api, portal) 8f714c1: Label images with their commit and source",
		},
		{
			name: "notice without environment",
			render: func(environment string) (slack.Blocks, string) {
				req := trackeidNotice()
				req.Environment = environment
				return createDeployNoticeMessage(req, "")
			},
			header: `"text":"*trackeid* → ` + noticeCommit + `"`,
			text:   "Deploying trackeid (api, portal) 8b4d196: Retry registry calls (+1)",
		},
		{
			name:        "notice with environment",
			environment: "prod",
			render: func(environment string) (slack.Blocks, string) {
				req := trackeidNotice()
				req.Environment = environment
				return createDeployNoticeMessage(req, "")
			},
			header: `"text":"*trackeid · prod* → ` + noticeCommit + `"`,
			text:   "Deploying trackeid · prod (api, portal) 8b4d196: Retry registry calls (+1)",
		},
		{
			name:        "single resource notice",
			environment: "staging",
			render: func(environment string) (slack.Blocks, string) {
				return createDeployNoticeMessage(&types.Approval{
					Kind:          types.ApprovalKindNotice,
					Identifier:    types.NoticeIdentifier("deployment/selfled/selfled", noticeNewerRevision),
					NewVersion:    "main",
					NewRevision:   noticeNewerRevision,
					SourceURL:     "https://github.com/tr8s/selfled",
					CommitSubject: "Show the booking calendar",
					Environment:   environment,
					Members:       types.ApprovalMembers{{Identifier: "deployment/selfled/selfled", Name: "selfled"}},
				}, "")
			},
			header: `"text":"*selfled · staging* → <https://github.com/tr8s/selfled/commit/` + noticeNewerRevision + `|4c80a47>"`,
			text:   "Deploying selfled · staging 4c80a47: Show the booking calendar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, text := tt.render(tt.environment)
			if rendered := renderBlocks(t, blocks); !strings.Contains(rendered, tt.header) {
				t.Errorf("expected header %s in:\n%s", tt.header, rendered)
			}
			if text != tt.text {
				t.Errorf("notification text = %q, want %q", text, tt.text)
			}
		})
	}
}

func TestEnvironmentLabelIsEscaped(t *testing.T) {
	req := trackeidNotice()
	req.Environment = "<prod & co>"

	blocks, text := createDeployNoticeMessage(req, "")
	if rendered := renderBlocks(t, blocks); !strings.Contains(rendered, "*trackeid · &lt;prod &amp; co&gt;*") {
		t.Errorf("expected the escaped environment in the header:\n%s", rendered)
	}

	// Slack escapes the notification text when it is sent
	_, values, err := slack.UnsafeApplyMsgOptions("xoxb-test", "deploys", "https://slack.example.com/api/", noticeMessageOptions(blocks, text)...)
	if err != nil {
		t.Fatalf("failed to apply message options: %s", err)
	}
	if got := values.Get("text"); !strings.HasPrefix(got, "Deploying trackeid · &lt;prod &amp; co&gt; (api, portal)") {
		t.Errorf("expected the escaped environment in the notification text, got %q", got)
	}
}

func TestEnvironmentFieldInDefaultLayout(t *testing.T) {
	req := groupApproval()
	blocks, _ := (&Bot{name: "keel"}).createApprovalMessage("Approval required! :mega:", req)
	if rendered := renderBlocks(t, blocks); strings.Contains(rendered, "*Environment:*") {
		t.Errorf("did not expect an environment field without a label:\n%s", rendered)
	}

	req.Environment = "<prod>"
	blocks, _ = (&Bot{name: "keel"}).createApprovalMessage("Approval required! :mega:", req)
	if rendered := renderBlocks(t, blocks); !strings.Contains(rendered, `"text":"*Environment:*\n&lt;prod&gt;"`) {
		t.Errorf("expected the environment field:\n%s", rendered)
	}
}
