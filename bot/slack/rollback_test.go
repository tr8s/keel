package slack

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/types"
	"github.com/slack-go/slack"
)

// deployedGroupApproval - the trackeid group approval with the rollouts of both members, which can be rolled back
func deployedGroupApproval(apiState, portalState types.RolloutState) *types.Approval {
	started := time.Now().Add(-time.Hour)
	target := func(state types.RolloutState, name string) types.RolloutTarget {
		t := types.RolloutTarget{
			State:      state,
			Ready:      2,
			Desired:    2,
			StartedAt:  started,
			FinishedAt: started.Add(41 * time.Second),
			Containers: []types.RolloutContainer{{Name: "app", Image: "registry.example.com/tr8s/" + name + ":main", PreviousDigest: "sha256:96df4af0000000000000000000000000000000000000000000000000000000", NewDigest: "sha256:e3a91130000000000000000000000000000000000000000000000000000000"}},
		}
		if state == types.RolloutStateFailed {
			t.Ready, t.Reason, t.FinishedAt = 1, "ImagePullBackOff", started.Add(10*time.Minute)
		}
		if state == types.RolloutStateRolling {
			t.Ready, t.FinishedAt = 1, time.Time{}
		}
		return t
	}
	req := approvedGroupApproval(target(apiState, "trackeid-api"), target(portalState, "trackeid-portal"))
	req.ID = "0b5e4a2c-approval"
	return req
}

const (
	menuAccessory  = `"accessory":{"action_id":"approval_menu","confirm":`
	menuOptions    = `"options":[{"text":{"text":"View changes","type":"plain_text"},"url":"https://github.com/tr8s/trackeid/compare/5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b...8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910","value":"view_changes"},{"text":{"text":"Roll back…","type":"plain_text"},"value":"rollback 0b5e4a2c-approval"}],"type":"overflow"`
	menuRollback   = `"Roll back…"`
	rollbackButton = `"text":{"emoji":true,"text":"Roll back","type":"plain_text"}`
	buttonAction   = `"action_id":"rollback"`
	confirmText    = "Sets api, portal back to 5f55a51. Database changes are not rolled back."
)

func TestCompactRollback(t *testing.T) {
	tests := []struct {
		name     string
		approval func() *types.Approval
		contains []string
		excludes []string
	}{
		{
			name:     "live offers view changes and roll back in the overflow menu",
			approval: func() *types.Approval { return deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive) },
			contains: []string{
				`"text":":white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>","type":"mrkdwn"}`,
				menuAccessory,
				menuOptions,
				confirmText,
			},
			excludes: []string{rollbackButton, `"type":"actions"`},
		},
		{
			name:     "failed offers view changes and roll back in the overflow menu",
			approval: func() *types.Approval { return deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed) },
			contains: []string{":x: portal not ready after 10m · ImagePullBackOff", menuAccessory, menuOptions},
			excludes: []string{rollbackButton},
		},
		{
			name: "view changes falls back to the commit without the running revision",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.CurrentRevision = ""
				return req
			},
			contains: []string{`"url":"https://github.com/tr8s/trackeid/commit/8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910","value":"view_changes"`, menuRollback},
			excludes: []string{rollbackButton},
		},
		{
			name: "button instead of a menu without a link to the changes",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.SourceURL = "https://git.example.com/tr8s/trackeid"
				return req
			},
			contains: []string{
				`"elements":[{"text":":white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>","type":"mrkdwn"}],"type":"context"`,
				buttonAction,
				rollbackButton,
				`"value":"0b5e4a2c-approval"`,
				confirmText,
			},
			excludes: []string{`"type":"overflow"`, "View changes"},
		},
		{
			name: "nothing to roll back while rolling out",
			approval: func() *types.Approval {
				return deployedGroupApproval(types.RolloutStateRolling, types.RolloutStateLive)
			},
			contains: []string{":hourglass_flowing_sand: Rolling out · api 1/2 · portal 2/2"},
			excludes: []string{menuRollback, rollbackButton},
		},
		{
			name: "nothing to roll back once rolled back, also after the rollback went live",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				now := time.Now()
				req.RolledBackBy, req.RolledBackAt = "U02ROLLBACK", &now
				return req
			},
			contains: []string{
				":rewind: Rolled back to 5f55a51 by <@U02ROLLBACK> · database changes are not rolled back",
				":white_check_mark: Live on api, portal in 41s",
			},
			excludes: []string{menuRollback, rollbackButton, "approved by"},
		},
		{
			name: "refused rollback",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.RollbackFailure = "trackeid-api was updated again since"
				return req
			},
			contains: []string{":warning: Roll back failed: trackeid-api was updated again since"},
			excludes: []string{menuRollback, rollbackButton},
		},
		{
			name: "nothing to roll back without the previous images",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.Rollout[1].Containers[0].PreviousDigest = ""
				return req
			},
			contains: []string{":white_check_mark: Live on api, portal"},
			excludes: []string{menuRollback, rollbackButton},
		},
		{
			name: "nothing to roll back on a superseded approval",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.SupersededBy = "9c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
				return req
			},
			contains: []string{":fast_forward: Superseded by 9c1d2e3"},
			excludes: []string{menuRollback, rollbackButton},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, _ := createCompactBlockMessage(tt.approval())
			rendered := renderBlocks(t, blocks)
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

func TestRolloutFailureMessageOffersRollbackButton(t *testing.T) {
	blocks, _ := createRolloutFailureMessage(deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed))
	rendered := renderBlocks(t, blocks)
	for _, expected := range []string{"<@U01ABCDEF> :x: portal not ready after 10m · ImagePullBackOff", buttonAction, rollbackButton, confirmText} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("expected %q in: %s", expected, rendered)
		}
	}
	if strings.Contains(rendered, `"type":"overflow"`) {
		t.Errorf("expected a visible button in the failure reply, got: %s", rendered)
	}
}

func TestApprovalMenuActions(t *testing.T) {
	tests := []struct {
		name     string
		action   *slack.BlockAction
		rollback bool
	}{
		{
			name:     "roll back button",
			action:   &slack.BlockAction{ActionID: bot.RollbackResponseKeyword, Value: "0b5e4a2c-approval"},
			rollback: true,
		},
		{
			name:     "roll back in the menu",
			action:   &slack.BlockAction{ActionID: approvalMenuActionID, SelectedOption: slack.OptionBlockObject{Value: "rollback 0b5e4a2c-approval"}},
			rollback: true,
		},
		{
			name: "view changes in the menu is ignored",
			action: &slack.BlockAction{ActionID: approvalMenuActionID, SelectedOption: slack.OptionBlockObject{
				Value: viewChangesValue,
				URL:   "https://github.com/tr8s/trackeid/compare/5f55a51...8f714c1",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := make(chan *bot.ApprovalResponse, 1)
			b := &Bot{approvalsRespCh: responses}
			b.handleAction("U02ROLLBACK", tt.action)

			select {
			case resp := <-responses:
				if !tt.rollback {
					t.Fatalf("expected the action to be ignored, got %+v", resp)
				}
				if !resp.Rollback || resp.User != "U02ROLLBACK" || resp.Text != "rollback 0b5e4a2c-approval" {
					t.Errorf("expected a rollback request by U02ROLLBACK, got %+v", resp)
				}
			default:
				if tt.rollback {
					t.Fatal("expected a rollback request")
				}
			}
		})
	}

	// the approve button stays an approval
	if resp, ok := bot.IsApproval("U01ABCDEF", actionText(&slack.BlockAction{ActionID: bot.ApprovalResponseKeyword, Value: "group/trackeid/trackeid:e3a9113"})); !ok || resp.Rollback || resp.Status != types.ApprovalStatusApproved {
		t.Errorf("expected the approve button to stay an approval, got %+v", resp)
	}
}

func TestRollbackConfirmationFitsSlackLimit(t *testing.T) {
	if got := rollbackConfirmation(deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive), "trackeid").Text.Text; got != confirmText {
		t.Errorf("expected the short member list in full, got %q", got)
	}

	req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
	req.IncludesMigration = true
	req.Rollout = nil
	for i := 1; i <= 15; i++ {
		req.SetRolloutTarget(types.RolloutTarget{
			Identifier: fmt.Sprintf("deployment/trackeid/trackeid-worker-with-a-rather-long-name-%02d", i),
			Name:       fmt.Sprintf("trackeid-worker-with-a-rather-long-name-%02d", i),
			State:      types.RolloutStateLive,
			Containers: []types.RolloutContainer{{Name: "app", Image: "registry.example.com/tr8s/worker:main", PreviousDigest: "sha256:96df4af0"}},
		})
	}

	text := rollbackConfirmation(req, "trackeid").Text.Text
	if length := utf8.RuneCountInString(text); length > maxConfirmTextLength {
		t.Errorf("confirmation has %d characters, more than %d: %q", length, maxConfirmTextLength, text)
	}
	for _, expected := range []string{"Sets worker-with-a-rather-long-name-01, ", " more back to 5f55a51.", "This change included a database migration"} {
		if !strings.Contains(text, expected) {
			t.Errorf("expected %q in %q", expected, text)
		}
	}
}
