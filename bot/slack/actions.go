package slack

import (
	"fmt"

	"github.com/slack-go/slack"
)

const (
	// approvalMenuActionID - the action of the overflow menu of an approval message, whose options carry their
	// whole command, ie: "rollback_request <approval id>"
	approvalMenuActionID = "approval_menu"
	// viewChangesValue - the value of the menu option that only opens the link to the changes
	viewChangesValue = "view_changes"

	// rollbackRequestActionID - asks the user who acted to confirm a rollback, "rollback_request <approval id>"
	rollbackRequestActionID = "rollback_request"
	// rollbackConfirmActionID - the confirmed rollback, "rollback_confirm <approval id> <rollback fingerprint>"
	rollbackConfirmActionID = "rollback_confirm"
	// rollbackCancelActionID - the cancelled confirmation, "rollback_cancel <approval id>"
	rollbackCancelActionID = "rollback_cancel"
)

// actionText - the command an interactive element sends, ie: "rollback_request <approval id>". Buttons carry their
// value, menu options their whole command. Links, ie: View changes, send nothing, so they are acknowledged and
// ignored.
func actionText(blockAction *slack.BlockAction) string {
	if blockAction.ActionID == approvalMenuActionID {
		if blockAction.SelectedOption.Value == viewChangesValue {
			return ""
		}
		return blockAction.SelectedOption.Value
	}

	value := blockAction.Value
	if value == "" {
		value = blockAction.SelectedOption.Value
	}
	return fmt.Sprintf("%s %s", blockAction.ActionID, value)
}
