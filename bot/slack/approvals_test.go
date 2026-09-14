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
