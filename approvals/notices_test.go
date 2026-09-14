package approvals

import (
	"context"
	"testing"
	"time"

	"github.com/keel-hq/keel/types"
)

const (
	noticeRevisionA    = "8b4d196a2b3c4d5e6f708192a3b4c5d6e7f80910"
	noticeRevisionB    = "4c80a47c85d9e0f1a2b3c4d5e6f708192a3b4c5d"
	noticeRevisionLive = "1a2b3c4d5e6f708192a3b4c5d6e7f809108b4d19"
)

func noticeRequest(revision string) *types.Approval {
	return &types.Approval{
		Provider:    types.ProviderTypeKubernetes,
		Identifier:  types.NoticeIdentifier("group/trackeid/trackeid", revision),
		Group:       "trackeid/trackeid",
		NewVersion:  "main",
		NewRevision: revision,
	}
}

func noticeMember(name string) types.ApprovalMember {
	return types.ApprovalMember{
		Identifier: "deployment/trackeid/" + name,
		Name:       name,
		Repository: types.Repository{Name: "registry.example.com/tr8s/" + name, Tag: "main"},
	}
}

func newNoticeManager(t *testing.T) *DefaultManager {
	t.Helper()
	store, teardown := NewTestingUtils()
	t.Cleanup(teardown)
	return New(&Opts{Store: store})
}

func recordNotice(t *testing.T, am *DefaultManager, req *types.Approval, member string) *types.Approval {
	t.Helper()
	notice, err := am.RecordDeployNotice(req, noticeMember(member))
	if err != nil {
		t.Fatalf("failed to record the deploy notice: %s", err)
	}
	return notice
}

func setNoticeRollout(t *testing.T, am *DefaultManager, id, member string, state types.RolloutState) {
	t.Helper()
	_, err := am.UpdateRollout(id, func(approval *types.Approval) bool {
		approval.SetRolloutTarget(types.RolloutTarget{Identifier: "deployment/trackeid/" + member, Name: member, State: state, StartedAt: time.Now()})
		return true
	})
	if err != nil {
		t.Fatalf("failed to record the rollout: %s", err)
	}
}

func noticeByID(t *testing.T, am *DefaultManager, id string) *types.Approval {
	t.Helper()
	notice, err := am.GetByID(id)
	if err != nil {
		t.Fatalf("failed to get the deploy notice: %s", err)
	}
	return notice
}

func TestDeployNoticeLateMemberJoins(t *testing.T) {
	am := newNoticeManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updated, _ := am.SubscribeUpdated(ctx)

	first := recordNotice(t, am, noticeRequest(noticeRevisionA), "trackeid-api")
	if !first.IsNotice() || !first.Archived || !first.Deadline.After(time.Now()) {
		t.Fatalf("expected an archived deploy notice that is kept, got %+v", first)
	}
	setNoticeRollout(t, am, first.ID, "trackeid-api", types.RolloutStateRolling)

	late := recordNotice(t, am, noticeRequest(noticeRevisionA), "trackeid-portal")
	if late.ID != first.ID || len(late.Members) != 2 {
		t.Fatalf("expected the late member to join the notice, got %s with %d members", late.ID, len(late.Members))
	}

	// recorded, rollout started, member joined
	for i := 0; i < 3; i++ {
		select {
		case notice := <-updated:
			if !notice.IsNotice() {
				t.Errorf("expected a deploy notice to be published, got %+v", notice)
			}
		case <-time.After(time.Second):
			t.Fatalf("expected 3 published notice updates, got %d", i)
		}
	}

	// deploy notices are no approvals
	if _, err := am.Get(first.Identifier); err == nil {
		t.Error("expected the deploy notice not to be found as an approval")
	}
	listed, err := am.List()
	if err != nil {
		t.Fatalf("failed to list approvals: %s", err)
	}
	for _, approval := range listed {
		if approval.IsNotice() {
			t.Errorf("did not expect a deploy notice in the approvals list: %s", approval.Identifier)
		}
	}
	if _, err := am.RequestRollback(first.ID, "U01ABCDEF"); err == nil {
		t.Error("expected a deploy notice to refuse rollbacks")
	}
}

func TestDeployNoticeMemberDeployedAgainGetsNewNotice(t *testing.T) {
	am := newNoticeManager(t)
	first := recordNotice(t, am, noticeRequest(noticeRevisionA), "trackeid-api")
	setNoticeRollout(t, am, first.ID, "trackeid-api", types.RolloutStateLive)

	again := recordNotice(t, am, noticeRequest(noticeRevisionA), "trackeid-api")
	if again.ID == first.ID {
		t.Error("expected a member that finished rolling out to get a new notice")
	}
}

func TestDeployNoticeSupersedesRollingNotices(t *testing.T) {
	am := newNoticeManager(t)

	live := recordNotice(t, am, noticeRequest(noticeRevisionLive), "trackeid-api")
	setNoticeRollout(t, am, live.ID, "trackeid-api", types.RolloutStateLive)

	older := recordNotice(t, am, noticeRequest(noticeRevisionA), "trackeid-api")
	setNoticeRollout(t, am, older.ID, "trackeid-api", types.RolloutStateRolling)

	otherRequest := noticeRequest(noticeRevisionA)
	otherRequest.Identifier = types.NoticeIdentifier("deployment/trackeid/trackeid-worker", noticeRevisionA)
	otherRequest.Group = ""
	other := recordNotice(t, am, otherRequest, "trackeid-worker")
	setNoticeRollout(t, am, other.ID, "trackeid-worker", types.RolloutStateRolling)

	newer := recordNotice(t, am, noticeRequest(noticeRevisionB), "trackeid-portal")

	if got := noticeByID(t, am, older.ID).SupersededBy; got != noticeRevisionB {
		t.Errorf("expected the rolling notice to be superseded by the newer revision, got %q", got)
	}
	if got := noticeByID(t, am, live.ID).SupersededBy; got != "" {
		t.Errorf("did not expect a live notice to be superseded, got %q", got)
	}
	if got := noticeByID(t, am, other.ID).SupersededBy; got != "" {
		t.Errorf("did not expect the notice of another resource to be superseded, got %q", got)
	}
	if got := noticeByID(t, am, newer.ID).SupersededBy; got != "" {
		t.Errorf("did not expect the newer notice to be superseded, got %q", got)
	}
}
