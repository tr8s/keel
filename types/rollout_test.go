package types

import (
	"testing"
	"time"
)

func TestApprovalRolloutState(t *testing.T) {
	tests := []struct {
		name   string
		states []RolloutState
		want   RolloutState
	}{
		{name: "nothing rolled out", want: ""},
		{name: "rolling", states: []RolloutState{RolloutStateLive, RolloutStateRolling}, want: RolloutStateRolling},
		{name: "live", states: []RolloutState{RolloutStateLive, RolloutStateLive}, want: RolloutStateLive},
		{name: "failed wins", states: []RolloutState{RolloutStateRolling, RolloutStateFailed, RolloutStateLive}, want: RolloutStateFailed},
		{name: "replaced rollouts are left out", states: []RolloutState{RolloutStateReplaced, RolloutStateLive}, want: RolloutStateLive},
		{name: "only replaced rollouts", states: []RolloutState{RolloutStateReplaced}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approval := &Approval{}
			for i, state := range tt.states {
				approval.SetRolloutTarget(RolloutTarget{Identifier: string(rune('a' + i)), State: state})
			}
			if got := approval.RolloutState(); got != tt.want {
				t.Errorf("RolloutState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApprovalSetRolloutTargetReplacesSameResource(t *testing.T) {
	started := time.Now()
	approval := &Approval{}
	approval.SetRolloutTarget(RolloutTarget{Identifier: "deployment/ns/api", Marker: "1", State: RolloutStateLive, StartedAt: started, FinishedAt: started.Add(41 * time.Second)})
	approval.SetRolloutTarget(RolloutTarget{Identifier: "deployment/ns/portal", State: RolloutStateLive, StartedAt: started.Add(time.Second), FinishedAt: started.Add(30 * time.Second)})

	if got := approval.RolloutDuration(); got != 41*time.Second {
		t.Errorf("RolloutDuration() = %s, want 41s", got)
	}

	approval.SetRolloutTarget(RolloutTarget{Identifier: "deployment/ns/api", Marker: "2", State: RolloutStateRolling})
	if len(approval.Rollout) != 2 || approval.RolloutTarget("deployment/ns/api").Marker != "2" {
		t.Errorf("expected the rollout of the same resource to be replaced, got %+v", approval.Rollout)
	}
}
