package bot

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jinzhu/gorm/dialects/sqlite"

	"github.com/keel-hq/keel/approvals"
	"github.com/keel-hq/keel/pkg/store/sql"
	"github.com/keel-hq/keel/types"
)

// The approval bots neither post nor update deploy notices, nor reply about their failed rollouts: deploy notices have
// a poster of their own
func TestApprovalBotsIgnoreDeployNotices(t *testing.T) {
	store, err := sql.New(sql.Opts{DatabaseType: "sqlite3", URI: filepath.Join(t.TempDir(), "gorm.db")})
	if err != nil {
		t.Fatalf("failed to open the store: %s", err)
	}
	manager := approvals.New(&approvals.Opts{Store: store})
	bm := &BotManager{approvalsManager: manager}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handled := make(chan *types.Approval, 20)
	handle := func(approval *types.Approval) error {
		handled <- approval
		return nil
	}
	go bm.SubscribeForApprovals(ctx, handle)
	go bm.SubscribeForApprovalUpdates(ctx, handle)
	go bm.SubscribeForRolloutFailures(ctx, handle)
	// the subscriptions are made by the goroutines
	time.Sleep(100 * time.Millisecond)

	member := func(name string) types.ApprovalMember {
		return types.ApprovalMember{Identifier: "deployment/trackeid/" + name, Name: name}
	}
	notice, err := manager.RecordDeployNotice(&types.Approval{
		Provider:   types.ProviderTypeKubernetes,
		Identifier: types.NoticeIdentifier("deployment/trackeid/trackeid-api", "main"),
		NewVersion: "main",
	}, member("trackeid-api"))
	if err != nil {
		t.Fatalf("failed to record the deploy notice: %s", err)
	}
	if _, err := manager.UpdateRollout(notice.ID, func(approval *types.Approval) bool {
		approval.SetRolloutTarget(types.RolloutTarget{Identifier: "deployment/trackeid/trackeid-api", Name: "trackeid-api", State: types.RolloutStateFailed})
		return true
	}); err != nil {
		t.Fatalf("failed to record the failed rollout: %s", err)
	}

	approval, err := manager.RequestGroupApproval(&types.Approval{
		Provider:      types.ProviderTypeKubernetes,
		Identifier:    types.GroupApprovalIdentifier("trackeid", "trackeid", "main"),
		Group:         "trackeid/trackeid",
		NewVersion:    "main",
		VotesRequired: 1,
		Deadline:      time.Now().Add(time.Hour),
	}, member("trackeid-portal"))
	if err != nil {
		t.Fatalf("failed to request approval: %s", err)
	}

	select {
	case got := <-handled:
		if got.ID != approval.ID {
			t.Fatalf("expected only the approval to reach the bots, got %s", got.Identifier)
		}
	case <-time.After(time.Second):
		t.Fatal("expected the approval request to reach the bots")
	}
	select {
	case got := <-handled:
		t.Errorf("did not expect %s to reach the bots", got.Identifier)
	case <-time.After(100 * time.Millisecond):
	}
}
