package approvals

import (
	"context"
	"time"

	"github.com/keel-hq/keel/pkg/store"
	"github.com/keel-hq/keel/types"
)

// SubscribeRolloutFailed - subscribe for approvals whose approved update failed to roll out on a resource
func (m *DefaultManager) SubscribeRolloutFailed(ctx context.Context) (<-chan *types.Approval, error) {
	return m.subscribe(ctx, m.rolloutFailedCh), nil
}

// UpdateRollout - apply fn to the approval with the id, archived or not, and store the approval when fn reports a
// change. Subscribers are notified of the updated approval (SubscribeUpdated) and, when one more resource failed
// to roll out, of the failure (SubscribeRolloutFailed).
func (m *DefaultManager) UpdateRollout(id string, fn func(approval *types.Approval) bool) (*types.Approval, error) {
	m.mu.Lock()
	approval, err := m.getByID(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	failed := failedRollouts(approval)
	if !fn(approval) {
		m.mu.Unlock()
		return approval, nil
	}

	approval.UpdatedAt = time.Now()
	err = m.store.UpdateApproval(approval)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// subscribers are notified once the lock is released, processing their queue may need it
	m.publishUpdated(approval)
	if failedRollouts(approval) > failed {
		m.publish(m.rolloutFailedCh, approval)
	}

	return approval, nil
}

// SetApprovalMessage - record where a bot posted the message of the approval, so that it can update the message
// and reply in its thread later
func (m *DefaultManager) SetApprovalMessage(id, channel, timestamp string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	approval, err := m.getByID(id)
	if err != nil {
		return err
	}
	if approval.MessageChannel == channel && approval.MessageTimestamp == timestamp {
		return nil
	}

	approval.MessageChannel = channel
	approval.MessageTimestamp = timestamp
	return m.store.UpdateApproval(approval)
}

// getByID - get the approval with the id, archived or not
func (m *DefaultManager) getByID(id string) (*types.Approval, error) {
	if id == "" {
		return nil, store.ErrRecordNotFound
	}
	return m.store.GetApproval(&types.GetApprovalQuery{ID: id})
}

func failedRollouts(approval *types.Approval) int {
	failed := 0
	for _, target := range approval.Rollout {
		if target.State == types.RolloutStateFailed {
			failed++
		}
	}
	return failed
}
