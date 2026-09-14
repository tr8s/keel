package slack

import (
	"strings"
	"testing"

	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"
)

// mustBlocks - the blocks of a message, without its notification text
func mustBlocks(blocks slack.Blocks, _ string) slack.Blocks {
	return blocks
}

const (
	migrationWarning  = ":warning: Includes a database migration. Keel does not run migrations; apply it before approving."
	migrationRollback = "Roll back api, portal to 5f55a51? Database changes are not rolled back. This change included a database migration; rolling back the app does not undo it."
)

func TestCompactMigrationWarning(t *testing.T) {
	pending := groupApproval()
	pending.IncludesMigration = true
	rendered := renderBlocks(t, mustBlocks(createCompactBlockMessage(pending)))
	subject := strings.Index(rendered, "Label images with their commit and source")
	warning := strings.Index(rendered, migrationWarning)
	if subject == -1 || warning == -1 || warning < subject {
		t.Errorf("expected the migration warning under the subject, got: %s", rendered)
	}

	live := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	live.IncludesMigration = true
	rendered = renderBlocks(t, mustBlocks(createCompactBlockMessage(live)))
	if !strings.Contains(rendered, migrationWarning) {
		t.Errorf("expected the warning to stay once live, got: %s", rendered)
	}

	without := renderBlocks(t, mustBlocks(createCompactBlockMessage(groupApproval())))
	if strings.Contains(without, ":warning: Includes a database migration") {
		t.Errorf("did not expect a migration warning without the label, got: %s", without)
	}
}

func TestRollbackConfirmationMentionsMigration(t *testing.T) {
	with := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed)
	with.IncludesMigration = true
	if _, text := createRollbackConfirmation(with); text != migrationRollback {
		t.Errorf("confirmation = %q, want %q", text, migrationRollback)
	}

	if _, text := createRollbackConfirmation(deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed)); text != confirmQuestion {
		t.Errorf("confirmation = %q, want %q", text, confirmQuestion)
	}
}
