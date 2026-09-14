package slack

import (
	"strings"
	"testing"
	"time"

	"github.com/keel-hq/keel/bot"
	"github.com/keel-hq/keel/types"
)

// approvedGroupApproval - the trackeid group approval, approved and with the rollouts of both members
func approvedGroupApproval(api, portal types.RolloutTarget) *types.Approval {
	req := groupApproval()
	req.AddVoter("U01ABCDEF")
	req.VotesReceived = 1
	api.Identifier, api.Name = "deployment/trackeid/trackeid-api", "trackeid-api"
	portal.Identifier, portal.Name = "deployment/trackeid/trackeid-portal", "trackeid-portal"
	req.SetRolloutTarget(api)
	req.SetRolloutTarget(portal)
	return req
}

func TestCompactRolloutStatus(t *testing.T) {
	started := time.Now().Add(-time.Hour)
	approveButton := `"action_id":"` + bot.ApprovalResponseKeyword + `"`

	tests := []struct {
		name     string
		approval *types.Approval
		contains string
	}{
		{
			name: "rolling",
			approval: approvedGroupApproval(
				types.RolloutTarget{State: types.RolloutStateRolling, Ready: 2, Desired: 2, StartedAt: started},
				types.RolloutTarget{State: types.RolloutStateRolling, Ready: 1, Desired: 2, StartedAt: started},
			),
			contains: ":hourglass_flowing_sand: Rolling out · api 2/2 · portal 1/2",
		},
		{
			name: "live",
			approval: approvedGroupApproval(
				types.RolloutTarget{State: types.RolloutStateLive, Ready: 2, Desired: 2, StartedAt: started, FinishedAt: started.Add(41 * time.Second)},
				types.RolloutTarget{State: types.RolloutStateLive, Ready: 2, Desired: 2, StartedAt: started.Add(time.Second), FinishedAt: started.Add(35 * time.Second)},
			),
			contains: ":white_check_mark: Live on api, portal in 41s · approved by <@U01ABCDEF>",
		},
		{
			name: "failed",
			approval: approvedGroupApproval(
				types.RolloutTarget{State: types.RolloutStateLive, Ready: 2, Desired: 2, StartedAt: started, FinishedAt: started.Add(41 * time.Second)},
				types.RolloutTarget{State: types.RolloutStateFailed, Ready: 1, Desired: 2, Reason: "ImagePullBackOff", StartedAt: started, FinishedAt: started.Add(10 * time.Minute)},
			),
			contains: ":x: portal not ready after 10m · ImagePullBackOff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, _ := createCompactBlockMessage(tt.approval)
			rendered := renderBlocks(t, blocks)
			if !strings.Contains(rendered, tt.contains) {
				t.Errorf("expected %q in: %s", tt.contains, rendered)
			}
			if strings.Contains(rendered, approveButton) || strings.Contains(rendered, ":white_check_mark: Approved by") {
				t.Errorf("expected the rollout to replace the approval outcome, got: %s", rendered)
			}
		})
	}
}

func TestCreateRolloutFailureMessage(t *testing.T) {
	started := time.Now().Add(-time.Hour)
	req := approvedGroupApproval(
		types.RolloutTarget{State: types.RolloutStateLive, StartedAt: started, FinishedAt: started.Add(41 * time.Second)},
		types.RolloutTarget{State: types.RolloutStateFailed, Reason: "ImagePullBackOff", StartedAt: started, FinishedAt: started.Add(10 * time.Minute)},
	)

	blocks, text := createRolloutFailureMessage(req)
	rendered := renderBlocks(t, blocks)
	if !strings.Contains(rendered, "<@U01ABCDEF> :x: portal not ready after 10m · ImagePullBackOff") {
		t.Errorf("expected the approvers and the failure, got: %s", rendered)
	}
	if text != "Rollout of trackeid failed" {
		t.Errorf("unexpected notification text %q", text)
	}
}

func TestFormatDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		41 * time.Second:                      "41s",
		10 * time.Minute:                      "10m",
		90 * time.Second:                      "1m30s",
		time.Hour:                             "1h",
		1500 * time.Millisecond:               "2s",
		2*time.Hour + 3*time.Minute:           "2h3m",
		time.Hour + 4*time.Second:             "1h0m4s",
		10*time.Minute + 400*time.Millisecond: "10m",
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
