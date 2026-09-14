package slack

import (
	"strings"
	"testing"

	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/types"
)

func groupApproval() *types.Approval {
	const (
		currentRevision = "5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b"
		newRevision     = "8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910"
	)
	req := movingTagApproval()
	req.Identifier = "group/trackeid/trackeid:" + newRevision
	req.Group = "trackeid/trackeid"
	req.Members = types.ApprovalMembers{
		{Identifier: "deployment/trackeid/trackeid-api", Name: "trackeid-api"},
		{Identifier: "deployment/trackeid/trackeid-portal", Name: "trackeid-portal"},
	}
	req.SourceURL = "https://github.com/tr8s/trackeid"
	req.CurrentRevision = currentRevision
	req.NewRevision = newRevision
	req.CommitSubject = "Label images with their commit and source"
	req.CommitAuthor = "Tim Brandin"
	return req
}

func TestCreateCompactBlockMessageGroup(t *testing.T) {
	approveButton := `"action_id":"` + bot.ApprovalResponseKeyword + `"`

	tests := []struct {
		name     string
		approval func() *types.Approval
		text     string
		contains []string
		excludes []string
	}{
		{
			name:     "pending lists the members",
			approval: groupApproval,
			text:     "Deploy trackeid (api, portal) 8f714c1: Label images with their commit and source",
			contains: []string{
				"*trackeid* → <https://github.com/tr8s/trackeid/commit/8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910|8f714c1>",
				`"text":"Label images with their commit and source"`,
				"trackeid · api, portal · Tim Brandin · <https://github.com/tr8s/trackeid/compare/5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b...8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910|5f55a51…8f714c1>",
				approveButton,
			},
		},
		{
			name: "approved",
			approval: func() *types.Approval {
				req := groupApproval()
				req.AddVoter("U01ABCDEF")
				req.VotesReceived = 1
				return req
			},
			contains: []string{"trackeid · api, portal · Tim Brandin", ":white_check_mark: Approved by <@U01ABCDEF>"},
			excludes: []string{approveButton},
		},
		{
			name: "superseded",
			approval: func() *types.Approval {
				req := groupApproval()
				req.SupersededBy = "9c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
				req.Archived = true
				return req
			},
			contains: []string{":fast_forward: Superseded by 9c1d2e3"},
			excludes: []string{approveButton, "Approved"},
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

func TestCreateBlockMessageGroup(t *testing.T) {
	pending := renderBlocks(t, createBlockMessage("Approval required! :mega:", "keel", false, groupApproval()))
	if !strings.Contains(pending, "*Members:*\\ntrackeid-api, trackeid-portal") || !strings.Contains(pending, `"action_id":"`+bot.ApprovalResponseKeyword+`"`) {
		t.Errorf("expected the members and the buttons, got: %s", pending)
	}

	req := groupApproval()
	req.SupersededBy = "9c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
	superseded := renderBlocks(t, createBlockMessage("Change superseded! :fast_forward:", "keel", false, req))
	if strings.Contains(superseded, `"action_id":"`+bot.ApprovalResponseKeyword+`"`) {
		t.Errorf("did not expect buttons on a superseded approval, got: %s", superseded)
	}
}
