package slack

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"
)

// renderBlocks - serialize blocks the way they are sent to Slack, keeping <, > and & readable
func renderBlocks(t *testing.T, blocks slack.Blocks) string {
	t.Helper()
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(blocks.BlockSet); err != nil {
		t.Fatalf("failed to encode blocks: %s", err)
	}
	return buf.String()
}

func movingTagApproval() *types.Approval {
	return &types.Approval{
		Provider:       types.ProviderTypeKubernetes,
		Identifier:     "deployment/trackeid/trackeid-portal:main",
		Message:        "New image is available for resource trackeid/trackeid-portal.",
		CurrentVersion: "main",
		NewVersion:     "main",
		CurrentDigest:  "sha256:5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7",
		NewDigest:      "sha256:62c200e9a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5",
		VotesRequired:  1,
		Deadline:       time.Now().Add(time.Hour),
	}
}

func TestCreateBlockMessageCommands(t *testing.T) {
	req := movingTagApproval()

	shown := renderBlocks(t, createBlockMessage("Approval required! :mega:", "keel", true, req))
	if !strings.Contains(shown, bot.BotEventTextToResponse["help"][0]) || !strings.Contains(shown, "@keel get approvals") {
		t.Errorf("expected the command list to be shown, got: %s", shown)
	}

	hidden := renderBlocks(t, createBlockMessage("Approval required! :mega:", "keel", false, req))
	if strings.Contains(hidden, bot.BotEventTextToResponse["help"][0]) || strings.Contains(hidden, "@keel") {
		t.Errorf("expected the command list to be hidden, got: %s", hidden)
	}

	for _, expected := range []string{"*Delta:*", bot.ApprovalResponseKeyword, bot.RejectResponseKeyword} {
		if !strings.Contains(hidden, expected) {
			t.Errorf("expected %q in the message without commands, got: %s", expected, hidden)
		}
	}
}

func TestCreateBlockMessageChangeLinks(t *testing.T) {
	const (
		currentRevision = "5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b"
		newRevision     = "62c200e9a1b2c3d4e5f60718293a4b5c6d7e8f90"
	)

	tests := []struct {
		name      string
		sourceURL string
		current   string
		new       string
		contains  []string
		excludes  []string
	}{
		{
			name:      "commit and changes links",
			sourceURL: "https://github.com/tr8s/trackeid.git",
			current:   currentRevision,
			new:       newRevision,
			contains: []string{
				"main@sha256:5f55a51b -> main@sha256:62c200e9",
				"*Commit:*\\n<https://github.com/tr8s/trackeid/commit/" + newRevision + "|62c200e>",
				"*Changes:*\\n<https://github.com/tr8s/trackeid/compare/" + currentRevision + "..." + newRevision + "|5f55a51...62c200e>",
			},
		},
		{
			name:      "commit link only without the running revision",
			sourceURL: "https://bitbucket.org/selfleaders/valuestree",
			new:       newRevision,
			contains:  []string{"<https://bitbucket.org/selfleaders/valuestree/commits/" + newRevision + "|62c200e>"},
			excludes:  []string{"*Changes:*"},
		},
		{
			name:      "unknown host shows the revision as text",
			sourceURL: "https://git.example.com/tr8s/trackeid",
			current:   currentRevision,
			new:       newRevision,
			contains:  []string{"*Commit:*\\n`" + newRevision + "`"},
			excludes:  []string{"<https://", "*Changes:*"},
		},
		{
			name:     "no revision",
			excludes: []string{"*Commit:*", "*Changes:*"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := movingTagApproval()
			req.SourceURL = tt.sourceURL
			req.CurrentRevision = tt.current
			req.NewRevision = tt.new

			rendered := renderBlocks(t, createBlockMessage("Approval required! :mega:", "keel", false, req))
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
