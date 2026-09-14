package approvals

import (
	"context"
	"time"

	"github.com/keel-hq/keel/pkg/store"
	"github.com/keel-hq/keel/types"
)

// SubscribeRollback - subscribe for requests to roll back approved updates, used by the providers that apply them
func (m *DefaultManager) SubscribeRollback(ctx context.Context) (<-chan *types.Approval, error) {
	return m.subscribe(ctx, m.rollbackCh), nil
}

// RequestRollback - request that the updates approved by an approval are rolled back, and record who asked. The
// reference is the id or the identifier of the approval; an identifier refers to its latest approval that
// deployed something. The providers subscribed to rollbacks roll the resources back.
func (m *DefaultManager) RequestRollback(reference, actor string) (*types.Approval, error) {
	return m.requestRollback(reference, actor, "")
}

// RequestConfirmedRollback - request a rollback that a user confirmed for what the confirmation showed. It is refused
// with types.ErrRollbackChanged when the rollback fingerprint of the approval changed since, ie: the approval was
// rolled back or rolled out again.
func (m *DefaultManager) RequestConfirmedRollback(reference, actor, fingerprint string) (*types.Approval, error) {
	return m.requestRollback(reference, actor, fingerprint)
}

// GetByID - get the approval with the id, archived or not
func (m *DefaultManager) GetByID(id string) (*types.Approval, error) {
	return m.getByID(id)
}

func (m *DefaultManager) requestRollback(reference, actor, fingerprint string) (*types.Approval, error) {
	m.mu.Lock()
	approval, err := m.rollbackCandidate(reference)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := approval.RollbackError(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if fingerprint != "" && approval.RollbackFingerprint() != fingerprint {
		m.mu.Unlock()
		return nil, types.ErrRollbackChanged
	}

	now := time.Now()
	approval.RolledBackBy = actor
	approval.RolledBackAt = &now
	approval.UpdatedAt = now
	err = m.store.UpdateApproval(approval)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}

	m.addAuditEntry(approval, types.AuditActionApprovalRolledBack, actor)
	m.publish(m.rollbackCh, approval)

	return approval, nil
}

// rollbackCandidate - the approval with the id or, for an identifier, its latest approval that deployed something
func (m *DefaultManager) rollbackCandidate(reference string) (*types.Approval, error) {
	if approval, err := m.getByID(reference); err == nil {
		return approval, nil
	}

	var latest *types.Approval
	for _, archived := range []bool{false, true} {
		approvals, err := m.store.ListApprovals(&types.GetApprovalQuery{Identifier: reference, Archived: archived})
		if err != nil {
			return nil, err
		}
		for _, approval := range approvals {
			if approval.Identifier != reference || len(approval.Rollout) == 0 {
				continue
			}
			if latest == nil || approval.UpdatedAt.After(latest.UpdatedAt) {
				latest = approval
			}
		}
	}

	if latest == nil {
		return nil, store.ErrRecordNotFound
	}
	return latest, nil
}
