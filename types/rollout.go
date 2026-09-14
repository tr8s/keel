package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// RolloutState - progress of the rollout of a resource updated from an approval
type RolloutState string

// Available rollout states
const (
	RolloutStateRolling RolloutState = "rolling"
	RolloutStateLive    RolloutState = "live"
	RolloutStateFailed  RolloutState = "failed"
	// RolloutStateReplaced - a newer update of the resource took over before the rollout finished
	RolloutStateReplaced RolloutState = "replaced"
)

// RolloutTarget - a resource updated from an approval and the progress of its rollout
type RolloutTarget struct {
	// Identifier of the resource, ie: deployment/trackeid/trackeid-api
	Identifier string `json:"identifier"`
	// Name of the resource, ie: trackeid-api
	Name string `json:"name"`
	// Marker is the keel.sh/update-time value of the update, it tells the updated pod template apart
	Marker string `json:"marker,omitempty"`

	State RolloutState `json:"state"`
	// Ready and Desired count the replicas of the update
	Ready   int32 `json:"ready"`
	Desired int32 `json:"desired"`
	// Reason tells why the rollout failed, ie: ImagePullBackOff
	Reason string `json:"reason,omitempty"`

	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

// RolloutTargets is stored as a JSON blob
type RolloutTargets []RolloutTarget

func (t RolloutTargets) Value() (driver.Value, error) {
	return json.Marshal(t)
}

func (t *RolloutTargets) Scan(src interface{}) error {
	var source []byte
	switch value := src.(type) {
	case []byte:
		source = value
	case string:
		source = []byte(value)
	default:
		return errors.New("type assertion .([]byte) failed.")
	}
	return json.Unmarshal(source, t)
}

// RolloutTarget - the rollout of the resource, nil when the resource was not updated from the approval
func (a *Approval) RolloutTarget(identifier string) *RolloutTarget {
	for i := range a.Rollout {
		if a.Rollout[i].Identifier == identifier {
			return &a.Rollout[i]
		}
	}
	return nil
}

// SetRolloutTarget - add the rollout of a resource, or replace the previous rollout of the same resource
func (a *Approval) SetRolloutTarget(target RolloutTarget) {
	if existing := a.RolloutTarget(target.Identifier); existing != nil {
		*existing = target
		return
	}
	a.Rollout = append(a.Rollout, target)
}

// RolloutState - the state of the updates approved by the approval: failed once a resource failed to roll out,
// rolling while a resource rolls out, live once every resource is live. Rollouts taken over by a newer update
// are left out, and the state is empty when nothing rolled out.
func (a *Approval) RolloutState() RolloutState {
	var state RolloutState
	for _, target := range a.Rollout {
		switch target.State {
		case RolloutStateFailed:
			return RolloutStateFailed
		case RolloutStateRolling:
			state = RolloutStateRolling
		case RolloutStateLive:
			if state == "" {
				state = RolloutStateLive
			}
		}
	}
	return state
}

// RolloutDuration - the time from the first rollout start to the last rollout that went live
func (a *Approval) RolloutDuration() time.Duration {
	var started, finished time.Time
	for _, target := range a.Rollout {
		if target.State != RolloutStateLive {
			continue
		}
		if started.IsZero() || target.StartedAt.Before(started) {
			started = target.StartedAt
		}
		if target.FinishedAt.After(finished) {
			finished = target.FinishedAt
		}
	}
	if started.IsZero() {
		return 0
	}
	return finished.Sub(started)
}
