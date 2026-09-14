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
	migrationRollback = "Sets api, portal back to 5f55a51. Database changes are not rolled back. This change included a database migration; rolling back the app does not undo it."
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
	if !strings.Contains(rendered, migrationWarning) || !strings.Contains(rendered, migrationRollback) {
		t.Errorf("expected the warning to stay and the rollback confirmation to mention the migration, got: %s", rendered)
	}

	without := renderBlocks(t, mustBlocks(createCompactBlockMessage(groupApproval())))
	if strings.Contains(without, ":warning: Includes a database migration") {
		t.Errorf("did not expect a migration warning without the label, got: %s", without)
	}
}

func TestRollbackConfirmationMentionsMigration(t *testing.T) {
	with := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed)
	with.IncludesMigration = true
	rendered := renderBlocks(t, mustBlocks(createRolloutFailureMessage(with)))
	if !strings.Contains(rendered, migrationRollback) {
		t.Errorf("expected the confirmation to mention the migration, got: %s", rendered)
	}

	without := renderBlocks(t, mustBlocks(createRolloutFailureMessage(deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed))))
	if !strings.Contains(without, confirmText+`"`) || strings.Contains(without, "This change included a database migration") {
		t.Errorf("expected the confirmation without the migration sentence, got: %s", without)
	}
}
