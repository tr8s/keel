package approvals

import (
	"fmt"
	"time"

	"github.com/keel-hq/keel/types"
)

// noticeRetention - how long deploy notices are kept. Members of a change that update within that time join its
// notice; the expiry service deletes older notices.
const noticeRetention = 24 * time.Hour

// RecordDeployNotice - record the deploy notice of an update that needed no approval. The workloads of a resource or
// of an approval group that update to the same change share the notice identified by r.Identifier:
//   - member joins that notice while it is kept, unless member already finished rolling out with it
//   - otherwise r is recorded with member as its only member, and the notices of other changes of the same resource
//     or group that are still rolling out are marked as superseded by the new change
//
// Notices are archived from the start, so they are never taken for approvals. Subscribers are notified of every
// recorded or changed notice (SubscribeUpdated).
func (m *DefaultManager) RecordDeployNotice(r *types.Approval, member types.ApprovalMember) (*types.Approval, error) {
	m.mu.Lock()
	notice, updated, err := m.recordDeployNotice(r, member)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// subscribers are notified once the lock is released, processing their queue may need it
	for _, u := range updated {
		m.publishUpdated(u)
	}
	return notice, nil
}

func (m *DefaultManager) recordDeployNotice(r *types.Approval, member types.ApprovalMember) (*types.Approval, []*types.Approval, error) {
	notices, err := m.listNotices()
	if err != nil {
		return nil, nil, err
	}

	for _, notice := range notices {
		if notice.Identifier != r.Identifier || !canJoinNotice(notice, member) {
			continue
		}
		changed := notice.SetMember(member)
		// a migration in the image of any member concerns the whole change
		if r.IncludesMigration && !notice.IncludesMigration {
			notice.IncludesMigration = true
			changed = true
		}
		if !changed {
			return notice, nil, nil
		}
		notice.UpdatedAt = time.Now()
		if err := m.store.UpdateApproval(notice); err != nil {
			return nil, nil, err
		}
		return notice, []*types.Approval{notice}, nil
	}

	now := time.Now()
	key := types.NoticeKey(r.Identifier)
	var updated []*types.Approval
	for _, notice := range notices {
		if types.NoticeKey(notice.Identifier) != key || notice.Identifier == r.Identifier || notice.SupersededBy != "" || notice.Expired() {
			continue
		}
		if state := notice.RolloutState(); state != "" && state != types.RolloutStateRolling {
			continue
		}
		notice.SupersededBy = groupChange(r)
		notice.UpdatedAt = now
		if err := m.store.UpdateApproval(notice); err != nil {
			return nil, nil, err
		}
		updated = append(updated, notice)
	}

	r.Kind = types.ApprovalKindNotice
	r.Archived = true
	r.Members = types.ApprovalMembers{member}
	r.CreatedAt = now
	r.UpdatedAt = now
	r.Deadline = now.Add(noticeRetention)

	notice, err := m.store.CreateApproval(r)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to record deploy notice: %s", err)
	}
	return notice, append(updated, notice), nil
}

// listNotices - the deploy notices, most recently updated first
func (m *DefaultManager) listNotices() ([]*types.Approval, error) {
	archived, err := m.store.ListApprovals(&types.GetApprovalQuery{Archived: true})
	if err != nil {
		return nil, err
	}
	var notices []*types.Approval
	for _, approval := range archived {
		if approval.IsNotice() {
			notices = append(notices, approval)
		}
	}
	return notices, nil
}

// canJoinNotice - whether a member joins the notice of its change: the notice is kept and the member did not finish
// rolling out with it, ie: an image deployed again under the same tag gets a notice of its own
func canJoinNotice(notice *types.Approval, member types.ApprovalMember) bool {
	if notice.Expired() {
		return false
	}
	target := notice.RolloutTarget(member.Identifier)
	return target == nil || target.State == types.RolloutStateRolling
}
