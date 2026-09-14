package slack

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/keel-hq/keel/types"
)

func TestMigrationNote(t *testing.T) {
	const note = "It runs when the new pods start; no manual step."
	pending := groupApproval()
	pending.IncludesMigration = true

	// without a note the warning stays exactly as before
	rendered := renderBlocks(t, mustBlocks(createCompactBlockMessageWithNote(pending, "")))
	if !strings.Contains(rendered, `"text":"`+migrationWarning+`"`) {
		t.Errorf("expected the default migration warning, got: %s", rendered)
	}

	// a note replaces what approvers are told to do
	custom := renderBlocks(t, mustBlocks((&Bot{compactApprovals: true, migrationNote: note}).createApprovalMessage("", pending)))
	if !strings.Contains(custom, `"text":":warning: Includes a database migration. `+note+`"`) || strings.Contains(custom, "Keel does not run migrations") {
		t.Errorf("expected the configured migration note, got: %s", custom)
	}

	// a long note is cut in the message
	long := strings.Repeat("The migration runs when the new pods start. ", 20)
	warning := migrationWarningText(long)
	prefix := ":warning: Includes a database migration. "
	if !strings.HasPrefix(warning, prefix) || utf8.RuneCountInString(strings.TrimPrefix(warning, prefix)) > maxMigrationNoteLength || !strings.HasSuffix(warning, "…") {
		t.Errorf("expected the note to be cut at %d characters, got %q", maxMigrationNoteLength, warning)
	}
}

func TestMigrationNoteDoesNotChangeTheRollbackConfirmation(t *testing.T) {
	live := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	live.IncludesMigration = true

	b := &Bot{compactApprovals: true, migrationNote: strings.Repeat("The migration runs when the new pods start. ", 20)}
	rendered := renderBlocks(t, mustBlocks(b.createApprovalMessage("", live)))
	if !strings.Contains(rendered, "The migration runs when the new pods start.") {
		t.Fatalf("expected the long note in the message, got: %s", rendered)
	}

	_, text := createRollbackConfirmation(live)
	if text != migrationRollback || utf8.RuneCountInString(text) > maxConfirmTextLength {
		t.Errorf("expected the rollback confirmation to keep its migration sentence within %d characters, got %q", maxConfirmTextLength, text)
	}
}
