package slack

import (
	"strings"
	"testing"
	"time"

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
	rollbackAction = `"action_id":"rollback"`
	rollbackMenu   = `"accessory":{"action_id":"rollback","confirm":`
	rollbackOption = `"options":[{"text":{"text":"Roll back…","type":"plain_text"},"value":"0b5e4a2c-approval"}],"type":"overflow"`
	rollbackButton = `"text":{"emoji":true,"text":"Roll back","type":"plain_text"}`
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
			name:     "live offers a roll back in the overflow menu",
			approval: func() *types.Approval { return deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive) },
			contains: []string{
				`"text":":white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>","type":"mrkdwn"}`,
				rollbackMenu,
				rollbackOption,
				confirmText,
			},
			excludes: []string{rollbackButton, `"type":"actions"`},
		},
		{
			name:     "failed offers a roll back in the overflow menu",
			approval: func() *types.Approval { return deployedGroupApproval(types.RolloutStateLive, types.RolloutStateFailed) },
			contains: []string{":x: portal not ready after 10m · ImagePullBackOff", rollbackMenu, rollbackOption},
			excludes: []string{rollbackButton},
		},
		{
			name: "no menu while rolling out",
			approval: func() *types.Approval {
				return deployedGroupApproval(types.RolloutStateRolling, types.RolloutStateLive)
			},
			contains: []string{":hourglass_flowing_sand: Rolling out · api 1/2 · portal 2/2"},
			excludes: []string{rollbackAction},
		},
		{
			name: "no menu once rolled back, also after the rollback went live",
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
			excludes: []string{rollbackAction, "approved by"},
		},
		{
			name: "refused rollback",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.RollbackFailure = "trackeid-api was updated again since"
				return req
			},
			contains: []string{":warning: Roll back failed: trackeid-api was updated again since"},
			excludes: []string{rollbackAction},
		},
		{
			name: "no menu without the previous images",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.Rollout[1].Containers[0].PreviousDigest = ""
				return req
			},
			contains: []string{":white_check_mark: Live on api, portal"},
			excludes: []string{rollbackAction},
		},
		{
			name: "no menu on a superseded approval",
			approval: func() *types.Approval {
				req := deployedGroupApproval(types.RolloutStateLive, types.RolloutStateLive)
				req.SupersededBy = "9c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
				return req
			},
			contains: []string{":fast_forward: Superseded by 9c1d2e3"},
			excludes: []string{rollbackAction},
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
	for _, expected := range []string{"<@U01ABCDEF> :x: portal not ready after 10m · ImagePullBackOff", rollbackAction, rollbackButton, confirmText} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("expected %q in: %s", expected, rendered)
		}
	}
	if strings.Contains(rendered, `"type":"overflow"`) {
		t.Errorf("expected a visible button in the failure reply, got: %s", rendered)
	}
}

func TestRollbackMenuIsHandledLikeTheButton(t *testing.T) {
	button := &slack.BlockAction{ActionID: bot.RollbackResponseKeyword, Value: "0b5e4a2c-approval"}
	menu := &slack.BlockAction{ActionID: bot.RollbackResponseKeyword, SelectedOption: slack.OptionBlockObject{Value: "0b5e4a2c-approval"}}

	for name, action := range map[string]*slack.BlockAction{"button": button, "overflow menu": menu} {
		text := actionText(action)
		resp, ok := bot.IsApproval("U02ROLLBACK", text)
		if text != "rollback 0b5e4a2c-approval" || !ok || !resp.Rollback || resp.User != "U02ROLLBACK" {
			t.Errorf("%s: expected a rollback request by U02ROLLBACK, got %q %+v", name, text, resp)
		}
	}

	approve := actionText(&slack.BlockAction{ActionID: bot.ApprovalResponseKeyword, Value: "group/trackeid/trackeid:e3a9113"})
	if resp, ok := bot.IsApproval("U01ABCDEF", approve); !ok || resp.Rollback || resp.Status != types.ApprovalStatusApproved {
		t.Errorf("expected the approve button to stay an approval, got %q %+v", approve, resp)
	}
}
