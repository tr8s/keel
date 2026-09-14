package slack

import (
	"fmt"

	"github.com/slack-go/slack"
)

const (
	// approvalMenuActionID - the action of the overflow menu of an approval message, whose options carry their
	// whole command, ie: "rollback <approval id>"
	approvalMenuActionID = "approval_menu"
	// viewChangesValue - the value of the menu option that only opens the link to the changes
	viewChangesValue = "view_changes"
)

// actionText - the command an interactive element sends, ie: "rollback <approval id>". Buttons carry their value,
// menu options their whole command. Links, ie: View changes, send nothing, so they are acknowledged and ignored.
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
