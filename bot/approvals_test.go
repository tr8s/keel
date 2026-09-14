package bot

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jinzhu/gorm/dialects/sqlite"

	"github.com/keel-hq/keel/approvals"
	"github.com/keel-hq/keel/pkg/store/sql"
	"github.com/keel-hq/keel/types"
)

func newApprovalsManager(t *testing.T) *approvals.DefaultManager {
	t.Helper()
	store, err := sql.New(sql.Opts{DatabaseType: "sqlite3", URI: filepath.Join(t.TempDir(), "gorm.db")})
	if err != nil {
		t.Fatalf("failed to create store: %s", err)
	}
	t.Cleanup(func() { store.Close() })
	return approvals.New(&approvals.Opts{Store: store})
}

func TestIsApprovalRollback(t *testing.T) {
	resp, ok := IsApproval("U02ROLLBACK", "rollback group/trackeid/trackeid:e3a9113")
	if !ok || !resp.Rollback || resp.User != "U02ROLLBACK" {
		t.Errorf("expected a rollback request by U02ROLLBACK, got %+v", resp)
	}
	if resp, ok := IsApproval("U01ABCDEF", "approve group/trackeid/trackeid:e3a9113"); !ok || resp.Rollback {
		t.Errorf("expected an approval, got %+v", resp)
	}
}

func TestProcessRollbackResponse(t *testing.T) {
	am := newApprovalsManager(t)
	err := am.Create(&types.Approval{
		Provider:      types.ProviderTypeKubernetes,
		Identifier:    "deployment/trackeid/trackeid-api:main",
		VotesRequired: 1,
		VotesReceived: 1,
		Deadline:      time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("failed to create approval: %s", err)
	}
	created, err := am.Get("deployment/trackeid/trackeid-api:main")
	if err != nil {
		t.Fatalf("failed to get approval: %s", err)
	}
	_, err = am.UpdateRollout(created.ID, func(approval *types.Approval) bool {
		approval.SetRolloutTarget(types.RolloutTarget{
			Identifier: "deployment/trackeid/trackeid-api",
			Name:       "trackeid-api",
			State:      types.RolloutStateLive,
			Containers: []types.RolloutContainer{{Name: "api", Image: "registry.example.com/tr8s/trackeid-api:main", PreviousDigest: "sha256:before", NewDigest: "sha256:after"}},
		})
		return true
	})
	if err != nil {
		t.Fatalf("failed to record the rollout: %s", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rollbacks, _ := am.SubscribeRollback(ctx)

	var replies []*types.Approval
	reply := func(approval *types.Approval) error {
		replies = append(replies, approval)
		return nil
	}
	bm := &BotManager{approvalsManager: am}

	// by identifier, as typed in "@keel rollback <identifier>"
	resp, _ := IsApproval("U02ROLLBACK", "rollback "+created.Identifier)
	if err := bm.processRollbackResponse(resp, reply); err != nil {
		t.Fatalf("failed to request the rollback: %s", err)
	}

	select {
	case requested := <-rollbacks:
		if requested.ID != created.ID || requested.RolledBackBy != "U02ROLLBACK" || requested.RolledBackAt == nil {
			t.Errorf("expected the rollback by U02ROLLBACK to be published, got %+v", requested)
		}
	case <-time.After(time.Second):
		t.Fatal("expected the rollback request to be published")
	}
	if len(replies) != 1 || replies[0].RolledBackBy != "U02ROLLBACK" {
		t.Errorf("expected the approval message to be updated once, got %d replies", len(replies))
	}

	// by id, as sent by the roll back button, once more
	resp, _ = IsApproval("U03OTHER", "rollback "+created.ID)
	if err := bm.processRollbackResponse(resp, reply); !errors.Is(err, types.ErrAlreadyRolledBack) {
		t.Errorf("expected a second rollback to be refused, got %v", err)
	}
}

func TestProcessConfirmedRollbackResponse(t *testing.T) {
	am := newApprovalsManager(t)
	err := am.Create(&types.Approval{
		Provider:      types.ProviderTypeKubernetes,
		Identifier:    "group/trackeid/trackeid:e3a9113",
		NewRevision:   "e3a9113",
		VotesRequired: 1,
		VotesReceived: 1,
		Deadline:      time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("failed to create approval: %s", err)
	}
	created, _ := am.Get("group/trackeid/trackeid:e3a9113")
	_, err = am.UpdateRollout(created.ID, func(approval *types.Approval) bool {
		approval.SetRolloutTarget(types.RolloutTarget{
			Identifier: "deployment/trackeid/trackeid-api",
			Name:       "trackeid-api",
			Marker:     "update",
			State:      types.RolloutStateLive,
			Containers: []types.RolloutContainer{{Name: "api", Image: "registry.example.com/tr8s/trackeid-api:main", PreviousDigest: "sha256:before"}},
		})
		return true
	})
	if err != nil {
		t.Fatalf("failed to record the rollout: %s", err)
	}

	bm := &BotManager{approvalsManager: am}
	reply := func(*types.Approval) error { return nil }

	// a confirmation shown for another state of the rollout is refused
	resp, _ := IsApproval("U02ROLLBACK", "rollback "+created.ID+" 0000000000000000")
	if err := bm.processRollbackResponse(resp, reply); !errors.Is(err, types.ErrRollbackChanged) {
		t.Fatalf("expected a stale confirmation to be refused, got %v", err)
	}
	if approval, _ := am.GetByID(created.ID); approval.RolledBackBy != "" {
		t.Fatalf("expected nothing to be rolled back, got %q", approval.RolledBackBy)
	}

	// the confirmation of the current state rolls back and records who confirmed
	current, _ := am.GetByID(created.ID)
	resp, _ = IsApproval("U02ROLLBACK", "rollback "+created.ID+" "+current.RollbackFingerprint())
	if err := bm.processRollbackResponse(resp, reply); err != nil {
		t.Fatalf("failed to roll back: %s", err)
	}
	if approval, _ := am.GetByID(created.ID); approval.RolledBackBy != "U02ROLLBACK" || approval.RolledBackAt == nil {
		t.Errorf("expected the rollback by U02ROLLBACK to be recorded, got %q", approval.RolledBackBy)
	}
}
