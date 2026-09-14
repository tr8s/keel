package kubernetes

import (
	"testing"
	"time"

	"github.com/keel-hq/keel/types"

	apps_v1 "k8s.io/api/apps/v1"
)

// restartedProvider - a new provider on the same cluster and approvals, as after a Keel restart
func restartedProvider(t *testing.T, p *Provider) *Provider {
	t.Helper()
	restarted, err := NewProvider(p.implementer, &fakeSender{}, p.approvalManager, p.cache)
	if err != nil {
		t.Fatalf("failed to get provider: %s", err)
	}
	restarted.rollouts.interval = 10 * time.Millisecond
	return restarted
}

func TestRestartResumesRolloutInProgress(t *testing.T) {
	f := newRolloutFixture(t, "1")
	// the provider that applied the update never checks its rollout again, as if Keel stopped
	f.provider.rollouts.interval = time.Hour
	id := f.approveUpdate()
	f.waitForRollout(id, stateIs(types.RolloutStateRolling))

	f.observe(apps_v1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2})

	restarted := restartedProvider(t, f.provider)
	restarted.ResumeRollouts(0)
	go restarted.Start()
	t.Cleanup(restarted.Stop)

	f.waitForRollout(id, stateIs(types.RolloutStateLive))
}

func TestRestartClosesTimedOutRollout(t *testing.T) {
	f := newRolloutFixture(t, "1")
	f.provider.rollouts.interval = time.Hour
	id := f.approveUpdate()

	restarted := restartedProvider(t, f.provider)
	restarted.SetRolloutTimeout(time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	restarted.resumeRollouts()

	failed := f.waitForRollout(id, stateIs(types.RolloutStateFailed))
	if target := failed.RolloutTarget("deployment/trackeid/trackeid-api"); target.Reason != "rollout timed out" {
		t.Errorf("expected the stale rollout to be closed as timed out, got %q", target.Reason)
	}
}

func TestRestartAppliesPendingRollback(t *testing.T) {
	f, previous := deployRevisionA(t)
	requested, err := f.provider.approvalManager.RequestRollback("group/trackeid/trackeid:"+groupRevisionA, "U02ROLLBACK")
	if err != nil {
		t.Fatalf("failed to request the rollback: %s", err)
	}

	// Keel restarts before the rollback is applied
	restarted := restartedProvider(t, f.provider)
	restarted.resumeRollouts()

	updates := f.syncUpdates()
	if len(updates) != 2 {
		t.Fatalf("expected the pending rollback to be applied to both members, got %d updates", len(updates))
	}
	for name, digest := range previous {
		if got := updates[name].Containers()[0].Image; got != "registry.example.com/tr8s/"+name+"@"+digest {
			t.Errorf("expected %s to be pinned to its previous digest, got %s", name, got)
		}
	}
	if approval := f.approvalByID(requested.ID); approval.RolloutState() != types.RolloutStateRolling || approval.RolledBackBy != "U02ROLLBACK" {
		t.Errorf("expected the rollback to roll out, got %q by %q", approval.RolloutState(), approval.RolledBackBy)
	}

	// resuming once more does not roll back twice
	restarted.resumeRollouts()
	if updates := f.syncUpdates(); len(updates) != 0 {
		t.Errorf("expected the applied rollback not to be applied again, got %d updates", len(updates))
	}
}
