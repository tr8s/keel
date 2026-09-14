package slack

import (
	"fmt"

	"github.com/slack-go/slack"
)

// actionText - the command an interactive element sends, ie: "rollback <approval id>". Buttons carry their value,
// overflow menus the value of the selected option.
func actionText(blockAction *slack.BlockAction) string {
	value := blockAction.Value
	if value == "" {
		value = blockAction.SelectedOption.Value
	}
	return fmt.Sprintf("%s %s", blockAction.ActionID, value)
}
