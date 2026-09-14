package kubernetes

import (
	"strings"
	"testing"
	"time"

	"github.com/keel-hq/keel/types"

	apps_v1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// noticeDeployment - a deployment of the trackeid namespace that needs no approval, with the annotations
func noticeDeployment(name, group string, annotations map[string]string) *apps_v1.Deployment {
	deployment := groupDeployment(name, group, "0")
	for key, value := range annotations {
		deployment.Annotations[key] = value
	}
	return deployment
}

// newNoticeFixture - a group fixture with deploy notices on
func newNoticeFixture(t *testing.T, deployments ...*apps_v1.Deployment) *groupFixture {
	t.Helper()
	f := newGroupFixture(t, deployments...)
	f.provider.EnableDeployNotices()
	t.Cleanup(f.provider.Stop)

	// nothing reads the published updates in these tests
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-f.updates:
			case <-done:
				return
			}
		}
	}()
	return f
}

// notices - the recorded deploy notices by identifier
func (f *groupFixture) notices() map[string]*types.Approval {
	f.t.Helper()
	rollouts, err := f.provider.approvalManager.ListRollouts()
	if err != nil {
		f.t.Fatalf("failed to list rollouts: %s", err)
	}
	notices := make(map[string]*types.Approval)
	for _, approval := range rollouts {
		if approval.IsNotice() {
			notices[approval.Identifier] = approval
		}
	}
	return notices
}

func (f *groupFixture) lastNotification() types.EventNotification {
	sender := f.provider.sender.(*fakeSender)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.sentEvent
}

func TestDeployNoticeForUpdateWithoutApproval(t *testing.T) {
	f := newNoticeFixture(t, noticeDeployment("trackeid-api", "", map[string]string{
		types.KeelNotificationChanAnnotation: "#trackeid-deploys, #other",
	}))

	if updated := f.push("trackeid-api", groupDigest("a"), groupRevisionA); strings.Join(updated, ",") != "trackeid-api" {
		t.Fatalf("expected the update to be applied, got %v", updated)
	}

	notice := f.notices()["notice/deployment/trackeid/trackeid-api:"+groupRevisionA]
	if notice == nil {
		t.Fatalf("expected a deploy notice for the revision, got %v", f.notices())
	}
	if notice.MessageChannel != "trackeid-deploys" || notice.Group != "" || notice.NewRevision != groupRevisionA {
		t.Errorf("unexpected deploy notice: %+v", notice)
	}
	if target := notice.RolloutTarget("deployment/trackeid/trackeid-api"); target == nil || target.State != types.RolloutStateRolling {
		t.Errorf("expected the rollout to be followed on the notice, got %+v", notice.Rollout)
	}

	// the Slack notification sender leaves the success notification out
	if event := f.lastNotification(); event.Level != types.LevelSuccess || event.Metadata[types.DeployNoticeMetadataKey] != "true" {
		t.Errorf("expected the success notification to be marked as reported by the deploy notice, got %+v", event)
	}
	if approvals, _ := f.provider.approvalManager.List(); len(approvals) != 0 {
		t.Errorf("did not expect approvals, got %d", len(approvals))
	}
}

func TestApprovalUpdateGetsNoDeployNotice(t *testing.T) {
	f := newNoticeFixture(t, groupDeployment("trackeid-api", "", "1"))

	if updated := f.push("trackeid-api", groupDigest("a"), groupRevisionA); len(updated) != 0 {
		t.Fatalf("expected the update to wait for approval, got %v", updated)
	}
	approval, err := f.provider.approvalManager.Get("deployment/trackeid/trackeid-api:main")
	if err != nil || approval.IsNotice() {
		t.Fatalf("expected an approval, got %+v, %v", approval, err)
	}

	if updated := f.approve(approval.Identifier); strings.Join(updated, ",") != "trackeid-api" {
		t.Fatalf("expected the approved update to be applied, got %v", updated)
	}
	if notices := f.notices(); len(notices) != 0 {
		t.Errorf("did not expect a deploy notice for an approved update, got %v", notices)
	}
	if event := f.lastNotification(); event.Level != types.LevelSuccess || event.Metadata[types.DeployNoticeMetadataKey] != "" {
		t.Errorf("expected the success notification of an approved update to be sent as before, got %+v", event)
	}
	if rolled := f.approvalByID(approval.ID); rolled.RolloutState() != types.RolloutStateRolling {
		t.Errorf("expected the rollout to be followed on the approval, got %q", rolled.RolloutState())
	}
}

func TestNoDeployNoticeWhenDisabled(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "", "0"))
	t.Cleanup(f.provider.Stop)

	if updated := f.push("trackeid-api", groupDigest("a"), groupRevisionA); strings.Join(updated, ",") != "trackeid-api" {
		t.Fatalf("expected the update to be applied, got %v", updated)
	}
	if rollouts, _ := f.provider.approvalManager.ListRollouts(); len(rollouts) != 0 {
		t.Errorf("did not expect a rollout to be recorded, got %d", len(rollouts))
	}
	if event := f.lastNotification(); event.Metadata[types.DeployNoticeMetadataKey] != "" {
		t.Errorf("did not expect the success notification to be marked, got %+v", event)
	}
}

func TestDeployNoticeGroupLateMember(t *testing.T) {
	f := newNoticeFixture(t,
		noticeDeployment("trackeid-api", "trackeid", nil),
		noticeDeployment("trackeid-portal", "trackeid", nil),
	)

	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)

	notices := f.notices()
	notice := notices["notice/group/trackeid/trackeid:"+groupRevisionA]
	if len(notices) != 1 || notice == nil {
		t.Fatalf("expected one deploy notice for the group, got %v", notices)
	}
	if got := memberIdentifiers(notice); got != "deployment/trackeid/trackeid-api,deployment/trackeid/trackeid-portal" {
		t.Errorf("expected the late member to join the notice, got %s", got)
	}
	if notice.Group != "trackeid/trackeid" || notice.MessageChannel != "" || len(notice.Rollout) != 2 {
		t.Errorf("unexpected group notice: group %q, channel %q, %d rollouts", notice.Group, notice.MessageChannel, len(notice.Rollout))
	}
}

func TestDeployNoticeSupersededByNewerRevision(t *testing.T) {
	f := newNoticeFixture(t, noticeDeployment("trackeid-api", "", nil))

	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.syncUpdates()
	if updated := f.push("trackeid-api", groupDigest("b"), groupRevisionB); strings.Join(updated, ",") != "trackeid-api" {
		t.Fatalf("expected the newer revision to be applied, got %v", updated)
	}

	notices := f.notices()
	older := notices["notice/deployment/trackeid/trackeid-api:"+groupRevisionA]
	newer := notices["notice/deployment/trackeid/trackeid-api:"+groupRevisionB]
	if older == nil || newer == nil {
		t.Fatalf("expected a notice for each revision, got %v", notices)
	}
	if older.SupersededBy != groupRevisionB {
		t.Errorf("expected the older notice to be superseded by the newer revision, got %q", older.SupersededBy)
	}
	if newer.SupersededBy != "" || newer.RolloutState() != types.RolloutStateRolling {
		t.Errorf("expected the newer notice to roll out, got superseded %q, state %q", newer.SupersededBy, newer.RolloutState())
	}
}

// deployWithoutApproval turns deploy notices on, applies an update of the api that needs no approval and returns the
// id of its notice
func (f *rolloutFixture) deployWithoutApproval() string {
	f.t.Helper()
	f.provider.EnableDeployNotices()
	event := types.Event{Repository: types.Repository{Name: "registry.example.com/tr8s/trackeid-api", Tag: "main", Digest: groupDigest("a")}}
	updated, err := f.provider.processEvent(&event)
	if err != nil || len(updated) != 1 {
		f.t.Fatalf("expected the update to be applied, got %v, %v", updated, err)
	}
	rollouts, err := f.approver.ListRollouts()
	if err != nil {
		f.t.Fatalf("failed to list rollouts: %s", err)
	}
	for _, approval := range rollouts {
		if approval.IsNotice() {
			return approval.ID
		}
	}
	f.t.Fatal("expected a deploy notice")
	return ""
}

func TestDeployNoticeRolloutGoesLive(t *testing.T) {
	f := newRolloutFixture(t, "0")
	id := f.deployWithoutApproval()

	f.observe(apps_v1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2})
	f.waitForRollout(id, stateIs(types.RolloutStateLive))

	time.Sleep(50 * time.Millisecond)
	updates, failures := f.published()
	if len(failures) != 0 {
		t.Errorf("did not expect a failure, got %d", len(failures))
	}
	if len(updates) == 0 || !updates[len(updates)-1].IsNotice() || updates[len(updates)-1].RolloutState() != types.RolloutStateLive {
		t.Errorf("expected the live notice to be published, got %d updates", len(updates))
	}
}

func TestDeployNoticeRolloutFails(t *testing.T) {
	f := newRolloutFixture(t, "0")
	f.implementer.podList = &v1.PodList{Items: []v1.Pod{{
		ObjectMeta: meta_v1.ObjectMeta{Name: "trackeid-api-new", Namespace: "trackeid", Labels: map[string]string{"app": "trackeid-api"}},
		Status: v1.PodStatus{ContainerStatuses: []v1.ContainerStatus{{
			Name:  "api",
			State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
		}}},
	}}}
	id := f.deployWithoutApproval()

	f.observe(apps_v1.DeploymentStatus{
		Replicas: 3, UpdatedReplicas: 1, ReadyReplicas: 2, AvailableReplicas: 2,
		Conditions: []apps_v1.DeploymentCondition{{Type: apps_v1.DeploymentProgressing, Status: v1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}},
	})
	failed := f.waitForRollout(id, stateIs(types.RolloutStateFailed))
	if target := failed.RolloutTarget("deployment/trackeid/trackeid-api"); target.Reason != "ImagePullBackOff" {
		t.Errorf("expected the pod reason, got %q", target.Reason)
	}

	time.Sleep(50 * time.Millisecond)
	if _, failures := f.published(); len(failures) != 1 || !failures[0].IsNotice() {
		t.Errorf("expected exactly one failure of the notice, got %d", len(failures))
	}
}

func TestRestartResumesDeployNotice(t *testing.T) {
	f := newRolloutFixture(t, "0")
	// the provider that applied the update never checks its rollout again, as if Keel stopped
	f.provider.rollouts.interval = time.Hour
	id := f.deployWithoutApproval()
	f.waitForRollout(id, stateIs(types.RolloutStateRolling))

	f.observe(apps_v1.DeploymentStatus{Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2})

	restarted := restartedProvider(t, f.provider)
	restarted.ResumeRollouts(0)
	go restarted.Start()
	t.Cleanup(restarted.Stop)

	f.waitForRollout(id, stateIs(types.RolloutStateLive))
}
