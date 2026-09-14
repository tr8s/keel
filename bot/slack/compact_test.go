package slack

import (
	"strings"
	"testing"
	"time"

	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/types"
)

func TestCreateCompactBlockMessage(t *testing.T) {
	const (
		currentRevision = "5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b"
		newRevision     = "8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910"
		subject         = "Label images with their commit and source"
	)
	approveButton := `"action_id":"` + bot.ApprovalResponseKeyword + `"`
	rejectButton := `"action_id":"` + bot.RejectResponseKeyword + `"`

	labelled := func() *types.Approval {
		req := movingTagApproval()
		req.SourceURL = "https://github.com/tr8s/trackeid"
		req.CurrentRevision = currentRevision
		req.NewRevision = newRevision
		req.CommitSubject = subject
		req.CommitAuthor = "Tim Brandin"
		return req
	}

	tests := []struct {
		name     string
		approval func() *types.Approval
		text     string
		contains []string
		excludes []string
	}{
		{
			name:     "pending with all labels",
			approval: labelled,
			text:     "Deploy trackeid-portal 8f714c1: " + subject,
			contains: []string{
				"*trackeid-portal* → <https://github.com/tr8s/trackeid/commit/" + newRevision + "|8f714c1>",
				`"text":"` + subject + `"`,
				"trackeid · Tim Brandin · <https://github.com/tr8s/trackeid/compare/" + currentRevision + "..." + newRevision + "|5f55a51…8f714c1>",
				approveButton,
				rejectButton,
			},
			excludes: []string{"votes", "*Delta:*", "*Identifier:*", "New image is available", "@keel", "Approved"},
		},
		{
			name:     "pending with no labels",
			approval: movingTagApproval,
			text:     "Deploy trackeid-portal sha256:62c200e9",
			contains: []string{"*trackeid-portal* → `sha256:62c200e9`", `"text":"trackeid"`, approveButton, rejectButton},
			excludes: []string{"<https://", "votes", "·"},
		},
		{
			name: "revision on an unknown host",
			approval: func() *types.Approval {
				req := labelled()
				req.SourceURL = "https://git.example.com/tr8s/trackeid"
				return req
			},
			contains: []string{"*trackeid-portal* → `8f714c1`", "trackeid · Tim Brandin\""},
			excludes: []string{"<https://"},
		},
		{
			name: "approved with voters",
			approval: func() *types.Approval {
				req := labelled()
				req.AddVoter("U01ABCDEF")
				req.AddVoter("admin")
				req.VotesReceived = 1
				return req
			},
			contains: []string{"*trackeid-portal* → <https://github.com/tr8s/trackeid/commit/", subject, ":white_check_mark: Approved by <@U01ABCDEF>, admin"},
			excludes: []string{approveButton, rejectButton},
		},
		{
			name: "rejected",
			approval: func() *types.Approval {
				req := labelled()
				req.Rejected = true
				return req
			},
			contains: []string{":x: Rejected"},
			excludes: []string{approveButton, rejectButton, "Approved"},
		},
		{
			name: "expired",
			approval: func() *types.Approval {
				req := labelled()
				req.Deadline = time.Now().Add(-time.Hour)
				return req
			},
			contains: []string{":hourglass: Expired"},
			excludes: []string{approveButton, rejectButton},
		},
		{
			name: "subject escaping",
			approval: func() *types.Approval {
				req := labelled()
				req.CommitSubject = "Render <b> & <@U123> > plain"
				return req
			},
			text:     "Deploy trackeid-portal 8f714c1: Render <b> & <@U123> > plain",
			contains: []string{`"text":"Render &lt;b&gt; &amp; &lt;@U123&gt; &gt; plain"`},
			excludes: []string{"<b>", "<@U123>"},
		},
		{
			name: "votes shown when more than one is required",
			approval: func() *types.Approval {
				req := labelled()
				req.VotesRequired = 2
				req.VotesReceived = 1
				return req
			},
			contains: []string{"· 1/2 votes", approveButton},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, text := createCompactBlockMessage(tt.approval())
			rendered := renderBlocks(t, blocks)

			if tt.text != "" && text != tt.text {
				t.Errorf("notification text = %q, want %q", text, tt.text)
			}
			for _, expected := range tt.contains {
				if !strings.Contains(rendered, expected) {
					t.Errorf("expected %q in: %s", expected, rendered)
				}
			}
			for _, unexpected := range tt.excludes {
				if strings.Contains(rendered, unexpected) {
					t.Errorf("did not expect %q in: %s", unexpected, rendered)
				}
			}
		})
	}
}

func TestCompactSubjectIsCut(t *testing.T) {
	req := movingTagApproval()
	req.CommitSubject = strings.Repeat("a", maxCommitSubjectLength+50)

	_, text := createCompactBlockMessage(req)
	if want := "Deploy trackeid-portal sha256:62c200e9: " + strings.Repeat("a", maxCommitSubjectLength-1) + "…"; text != want {
		t.Errorf("notification text = %q, want %q", text, want)
	}
}

func TestCreateApprovalMessageLayout(t *testing.T) {
	req := movingTagApproval()

	defaultBlocks, defaultText := (&Bot{name: "keel"}).createApprovalMessage("Approval required! :mega:", req)
	if defaultText != "" || !strings.Contains(renderBlocks(t, defaultBlocks), "*Delta:*") {
		t.Errorf("expected the default layout without notification text, got %q: %s", defaultText, renderBlocks(t, defaultBlocks))
	}

	compactBlocks, compactText := (&Bot{name: "keel", compactApprovals: true}).createApprovalMessage("Approval required! :mega:", req)
	rendered := renderBlocks(t, compactBlocks)
	if compactText == "" || strings.Contains(rendered, "*Delta:*") || strings.Contains(rendered, "@keel") {
		t.Errorf("expected the compact layout without commands, got %q: %s", compactText, rendered)
	}
}
