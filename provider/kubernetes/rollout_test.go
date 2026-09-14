package kubernetes

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/keel-hq/keel/approvals"
	"github.com/keel-hq/keel/internal/k8s"
	"github.com/keel-hq/keel/types"

	apps_v1 "k8s.io/api/apps/v1"
	batch_v1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func replicaCount(replicas int32) *int32 {
	return &replicas
}

func TestEvaluateRollout(t *testing.T) {
	deployment := func(marker string, generation int64, status apps_v1.DeploymentStatus) *k8s.GenericResource {
		gr, err := k8s.NewGenericResource(&apps_v1.Deployment{
			ObjectMeta: meta_v1.ObjectMeta{Name: "trackeid-api", Namespace: "trackeid", Generation: generation},
			Spec: apps_v1.DeploymentSpec{
				Replicas: replicaCount(2),
				Template: v1.PodTemplateSpec{ObjectMeta: meta_v1.ObjectMeta{Annotations: map[string]string{types.KeelUpdateTimeAnnotation: marker}}},
			},
			Status: status,
		})
		if err != nil {
			t.Fatal(err)
		}
		return gr
	}
	resource := func(obj interface{}) *k8s.GenericResource {
		gr, err := k8s.NewGenericResource(obj)
		if err != nil {
			t.Fatal(err)
		}
		return gr
	}

	updatedTemplate := v1.PodTemplateSpec{ObjectMeta: meta_v1.ObjectMeta{Annotations: map[string]string{types.KeelUpdateTimeAnnotation: "update"}}}

	tests := []struct {
		name     string
		resource *k8s.GenericResource
		want     rolloutProgress
	}{
		{name: "resource not found", resource: nil, want: rolloutProgress{}},
		{
			name:     "update not observed by the cache yet",
			resource: deployment("before", 2, apps_v1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2}),
			want:     rolloutProgress{},
		},
		{
			name:     "update not observed by the controller yet",
			resource: deployment("update", 3, apps_v1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2}),
			want:     rolloutProgress{Desired: 2},
		},
		{
			name:     "rolling",
			resource: deployment("update", 3, apps_v1.DeploymentStatus{ObservedGeneration: 3, Replicas: 3, UpdatedReplicas: 1, ReadyReplicas: 3, AvailableReplicas: 3}),
			want:     rolloutProgress{Ready: 1, Desired: 2},
		},
		{
			name:     "live",
			resource: deployment("update", 3, apps_v1.DeploymentStatus{ObservedGeneration: 3, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2}),
			want:     rolloutProgress{Ready: 2, Desired: 2, Done: true},
		},
		{
			name: "progress deadline exceeded",
			resource: deployment("update", 3, apps_v1.DeploymentStatus{
				ObservedGeneration: 3, Replicas: 3, UpdatedReplicas: 1, ReadyReplicas: 2, AvailableReplicas: 2,
				Conditions: []apps_v1.DeploymentCondition{{Type: apps_v1.DeploymentProgressing, Status: v1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}},
			}),
			want: rolloutProgress{Ready: 1, Desired: 2, Failure: "ProgressDeadlineExceeded"},
		},
		{
			name: "stateful set live",
			resource: resource(&apps_v1.StatefulSet{
				ObjectMeta: meta_v1.ObjectMeta{Name: "db", Namespace: "trackeid", Generation: 2},
				Spec:       apps_v1.StatefulSetSpec{Replicas: replicaCount(3), Template: updatedTemplate},
				Status:     apps_v1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 3, UpdatedReplicas: 3, ReadyReplicas: 3, CurrentRevision: "r2", UpdateRevision: "r2"},
			}),
			want: rolloutProgress{Ready: 3, Desired: 3, Done: true},
		},
		{
			name: "daemon set rolling",
			resource: resource(&apps_v1.DaemonSet{
				ObjectMeta: meta_v1.ObjectMeta{Name: "agent", Namespace: "trackeid", Generation: 2},
				Spec:       apps_v1.DaemonSetSpec{Template: updatedTemplate},
				Status:     apps_v1.DaemonSetStatus{ObservedGeneration: 2, DesiredNumberScheduled: 4, UpdatedNumberScheduled: 2, NumberAvailable: 3},
			}),
			want: rolloutProgress{Ready: 2, Desired: 4},
		},
		{
			name: "cron job has nothing to roll out",
			resource: resource(&batch_v1.CronJob{
				ObjectMeta: meta_v1.ObjectMeta{Name: "report", Namespace: "trackeid"},
				Spec: batch_v1.CronJobSpec{JobTemplate: batch_v1.JobTemplateSpec{
					ObjectMeta: updatedTemplate.ObjectMeta,
					Spec:       batch_v1.JobSpec{Template: updatedTemplate},
				}},
			}),
			want: rolloutProgress{Done: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluateRollout(tt.resource, "update"); got != tt.want {
				t.Errorf("evaluateRollout() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

type rolloutFixture struct {
	t           *testing.T
	provider    *Provider
	implementer *fakeImplementer
	cache       *k8s.GenericResourceCache
	approver    *approvals.DefaultManager

	mu       sync.Mutex
	updates  []*types.Approval
	failures []*types.Approval
}

func newRolloutFixture(t *testing.T, approvalsRequired string) *rolloutFixture {
	t.Helper()

	deployment := &apps_v1.Deployment{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:        "trackeid-api",
			Namespace:   "trackeid",
			Labels:      map[string]string{types.KeelPolicyLabel: "force", types.KeelForceTagMatchLabel: "true"},
			Annotations: map[string]string{types.KeelMinimumApprovalsLabel: approvalsRequired},
		},
		Spec: apps_v1.DeploymentSpec{
			Replicas: replicaCount(2),
			Selector: &meta_v1.LabelSelector{MatchLabels: map[string]string{"app": "trackeid-api"}},
			Template: v1.PodTemplateSpec{
				ObjectMeta: meta_v1.ObjectMeta{Labels: map[string]string{"app": "trackeid-api"}},
				Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "api", Image: "registry.example.com/tr8s/trackeid-api:main"}}},
			},
		},
	}

	fp := &fakeImplementer{
		namespaces: &v1.NamespaceList{Items: []v1.Namespace{{ObjectMeta: meta_v1.ObjectMeta{Name: "trackeid"}}}},
	}
	grc := &k8s.GenericResourceCache{}
	grc.Add(MustParseGRS([]*apps_v1.Deployment{deployment})...)

	approver, teardown := approver()
	t.Cleanup(teardown)

	provider, err := NewProvider(fp, &fakeSender{}, approver, grc)
	if err != nil {
		t.Fatalf("failed to get provider: %s", err)
	}
	provider.rollouts.interval = 10 * time.Millisecond
	t.Cleanup(provider.Stop)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updates, _ := approver.SubscribeUpdated(ctx)
	failures, _ := approver.SubscribeRolloutFailed(ctx)

	f := &rolloutFixture{t: t, provider: provider, implementer: fp, cache: grc, approver: approver}
	go func() {
		for {
			select {
			case approval := <-updates:
				f.mu.Lock()
				f.updates = append(f.updates, approval)
				f.mu.Unlock()
			case approval := <-failures:
				f.mu.Lock()
				f.failures = append(f.failures, approval)
				f.mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
	return f
}

// approveUpdate requests approval for a new image of the api, approves it, applies the approved update and
// returns the approval id
func (f *rolloutFixture) approveUpdate() string {
	f.t.Helper()
	event := types.Event{Repository: types.Repository{Name: "registry.example.com/tr8s/trackeid-api", Tag: "main", Digest: groupDigest("a")}}
	if _, err := f.provider.processEvent(&event); err != nil {
		f.t.Fatalf("failed to process event: %s", err)
	}
	approval, err := f.approver.Get("deployment/trackeid/trackeid-api:main")
	if err != nil {
		f.t.Fatalf("expected an approval: %s", err)
	}
	if _, err := f.approver.Approve(approval.Identifier, "U01ABCDEF"); err != nil {
		f.t.Fatalf("failed to approve: %s", err)
	}

	event.TriggerName = types.TriggerTypeApproval.String()
	updated, err := f.provider.processEvent(&event)
	if err != nil || len(updated) != 1 {
		f.t.Fatalf("expected the approved update to be applied, got %v, %v", updated, err)
	}
	return approval.ID
}

// observe makes the cache show the updated deployment with the status
func (f *rolloutFixture) observe(status apps_v1.DeploymentStatus) {
	f.implementer.mu.Lock()
	updated := f.implementer.updated.DeepCopy()
	f.implementer.mu.Unlock()

	deployment := updated.GetResource().(*apps_v1.Deployment)
	deployment.Generation = 2
	status.ObservedGeneration = 2
	deployment.Status = status
	f.cache.Add(updated)
}

func (f *rolloutFixture) waitForRollout(id string, done func(*types.Approval) bool) *types.Approval {
	f.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		approval, err := f.approver.UpdateRollout(id, func(*types.Approval) bool { return false })
		if err == nil && done(approval) {
			return approval
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatalf("rollout of approval %s did not reach the expected state", id)
	return nil
}

func (f *rolloutFixture) published() (updates, failures []*types.Approval) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*types.Approval(nil), f.updates...), append([]*types.Approval(nil), f.failures...)
}

func stateIs(state types.RolloutState) func(*types.Approval) bool {
	return func(approval *types.Approval) bool { return approval.RolloutState() == state }
}

func TestRolloutGoesLiveAfterApprovedUpdate(t *testing.T) {
	f := newRolloutFixture(t, "1")
	id := f.approveUpdate()

	approval := f.waitForRollout(id, stateIs(types.RolloutStateRolling))
	if target := approval.RolloutTarget("deployment/trackeid/trackeid-api"); target == nil || target.Name != "trackeid-api" || target.Marker == "" {
		t.Fatalf("expected the rollout of the api to be recorded, got %+v", approval.Rollout)
	}

	f.observe(apps_v1.DeploymentStatus{Replicas: 3, UpdatedReplicas: 1, ReadyReplicas: 3, AvailableReplicas: 3})
	f.waitForRollout(id, func(a *types.Approval) bool {
		target := a.RolloutTarget("deployment/trackeid/trackeid-api")
		return target.State == types.RolloutStateRolling && target.Ready == 1 && target.Desired == 2
	})

	f.observe(apps_v1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2})
	live := f.waitForRollout(id, stateIs(types.RolloutStateLive))
	if target := live.RolloutTarget("deployment/trackeid/trackeid-api"); target.Ready != 2 || target.FinishedAt.IsZero() {
		t.Errorf("unexpected live rollout: %+v", target)
	}

	time.Sleep(50 * time.Millisecond)
	updates, failures := f.published()
	if len(failures) != 0 {
		t.Errorf("did not expect a failure notification, got %d", len(failures))
	}
	if len(updates) == 0 || updates[len(updates)-1].RolloutState() != types.RolloutStateLive {
		t.Errorf("expected the live rollout to be published as an update, got %d updates", len(updates))
	}
}

func TestRolloutFailsOnProgressDeadline(t *testing.T) {
	f := newRolloutFixture(t, "1")
	f.implementer.podList = &v1.PodList{Items: []v1.Pod{{
		ObjectMeta: meta_v1.ObjectMeta{Name: "trackeid-api-new", Namespace: "trackeid", Labels: map[string]string{"app": "trackeid-api"}},
		Status: v1.PodStatus{ContainerStatuses: []v1.ContainerStatus{{
			Name:  "api",
			State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
		}}},
	}}}
	id := f.approveUpdate()

	f.observe(apps_v1.DeploymentStatus{
		Replicas: 3, UpdatedReplicas: 1, ReadyReplicas: 2, AvailableReplicas: 2,
		Conditions: []apps_v1.DeploymentCondition{{Type: apps_v1.DeploymentProgressing, Status: v1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}},
	})
	failed := f.waitForRollout(id, stateIs(types.RolloutStateFailed))
	if target := failed.RolloutTarget("deployment/trackeid/trackeid-api"); target.Reason != "ImagePullBackOff" {
		t.Errorf("expected the pod reason, got %q", target.Reason)
	}

	time.Sleep(50 * time.Millisecond)
	if _, failures := f.published(); len(failures) != 1 {
		t.Errorf("expected exactly one failure notification, got %d", len(failures))
	}
}

func TestRolloutFailsAfterTimeout(t *testing.T) {
	f := newRolloutFixture(t, "1")
	f.provider.SetRolloutTimeout(50 * time.Millisecond)
	id := f.approveUpdate()

	// the cache never shows the update
	failed := f.waitForRollout(id, stateIs(types.RolloutStateFailed))
	if target := failed.RolloutTarget("deployment/trackeid/trackeid-api"); target.Reason != "rollout timed out" {
		t.Errorf("expected a timeout, got %q", target.Reason)
	}
}

func TestNoRolloutWithoutApprovals(t *testing.T) {
	f := newRolloutFixture(t, "0")
	event := types.Event{Repository: types.Repository{Name: "registry.example.com/tr8s/trackeid-api", Tag: "main", Digest: groupDigest("a")}}
	updated, err := f.provider.processEvent(&event)
	if err != nil || len(updated) != 1 {
		t.Fatalf("expected the update to be applied, got %v, %v", updated, err)
	}

	time.Sleep(50 * time.Millisecond)
	f.provider.rollouts.mu.Lock()
	watches := len(f.provider.rollouts.watches)
	f.provider.rollouts.mu.Unlock()
	updates, failures := f.published()
	if watches != 0 || len(updates) != 0 || len(failures) != 0 {
		t.Errorf("did not expect a rollout to be followed, got %d watches, %d updates, %d failures", watches, len(updates), len(failures))
	}
}
