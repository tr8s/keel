package approvals

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/keel-hq/keel/pkg/store"
	"github.com/keel-hq/keel/types"

	log "github.com/sirupsen/logrus"
)

// Manager is used to manage updates
type Manager interface {
	// Subscribe for approval request events, subscriber should provide
	// its name. Indented to be used by extensions that collect
	// approvals
	Subscribe(ctx context.Context) (<-chan *types.Approval, error)

	// SubscribeApproved - is used to get approved events by the manager
	SubscribeApproved(ctx context.Context) (<-chan *types.Approval, error)

	// SubscribeUpdated - is used to get approvals that changed without a vote, ie: a workload joined a
	// group approval or a newer change superseded it
	SubscribeUpdated(ctx context.Context) (<-chan *types.Approval, error)

	// request approval for deployment/release/etc..
	Create(r *types.Approval) error
	// Update whole approval object
	Update(r *types.Approval) error
	// SetRequiredVotes updates pending approvals for a resource and reevaluates their status.
	SetRequiredVotes(resourceIdentifier string, provider types.ProviderType, votesRequired int) error

	// RequestGroupApproval requests approval for a workload of an approval group, sharing one approval
	// between the workloads of the group that update to the same change
	RequestGroupApproval(r *types.Approval, member types.ApprovalMember) (*types.Approval, error)
	// SetGroupMemberDeployed records that a member of a group approval was updated
	SetGroupMemberDeployed(identifier, memberIdentifier string) error

	// UpdateRollout applies fn to the approval with the id, archived or not, to record the rollout of the
	// updates it approved
	UpdateRollout(id string, fn func(approval *types.Approval) bool) (*types.Approval, error)
	// SubscribeRolloutFailed - is used to get approvals whose approved update failed to roll out
	SubscribeRolloutFailed(ctx context.Context) (<-chan *types.Approval, error)
	// SetApprovalMessage records where a bot posted the message of an approval
	SetApprovalMessage(id, channel, timestamp string) error

	// Increases Approval votes by 1
	Approve(identifier, voter string) (*types.Approval, error)
	// Rejects Approval
	Reject(identifier string) (*types.Approval, error)

	// If the approval exists
	Exists(identifier string) bool

	Get(identifier string) (*types.Approval, error)
	List() ([]*types.Approval, error)
	Delete(*types.Approval) error
	Archive(identifier string) error

	StartExpiryService(ctx context.Context) error
}

// Approvals related errors
var (
	ErrApprovalAlreadyExists = errors.New("approval already exists")
)

// Approvals cache prefix
const (
	ApprovalsPrefix = "approvals"
)

// DefaultManager - default manager implementation
type DefaultManager struct {
	// cache is used to store approvals, key example:
	// approvals/<provider name>/<identifier>
	// cache cache.Cache

	store store.Store

	// subscriber channels
	channels map[uint32]chan *types.Approval
	index    uint32

	// approved channels
	approvedCh map[uint32]chan *types.Approval

	// updated channels
	updatedCh map[uint32]chan *types.Approval

	// rollout failure channels
	rolloutFailedCh map[uint32]chan *types.Approval

	mu    *sync.Mutex
	subMu *sync.RWMutex
}

type Opts struct {
	Store store.Store
	// Cache cache.Cache
}

// New create new instance of default manager
func New(opts *Opts) *DefaultManager {
	man := &DefaultManager{
		// cache:      opts.Cache,
		store:           opts.Store,
		channels:        make(map[uint32]chan *types.Approval),
		approvedCh:      make(map[uint32]chan *types.Approval),
		updatedCh:       make(map[uint32]chan *types.Approval),
		rolloutFailedCh: make(map[uint32]chan *types.Approval),
		index:           0,
		mu:              &sync.Mutex{},
		subMu:           &sync.RWMutex{},
	}

	return man
}

// StartExpiryService - starts approval expiry service which deletes approvals
// that already reached their deadline
func (m *DefaultManager) StartExpiryService(ctx context.Context) error {
	ticker := time.NewTicker(60 * time.Minute)
	defer ticker.Stop()
	err := m.expireEntries()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Error("approvals.StartExpiryService: got error while performing initial expired approvals check")
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			err := m.expireEntries()
			if err != nil {
				log.WithFields(log.Fields{
					"error": err,
				}).Error("approvals.StartExpiryService: got error while performing routinely expired approvals check")
			}
		}
	}
}

func (m *DefaultManager) expireEntries() error {
	approvals, err := m.store.ListApprovals(&types.GetApprovalQuery{
		Archived: false,
	})
	if err != nil {
		return err
	}

	for _, approval := range approvals {
		if approval.Expired() {
			err = m.Delete(approval)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err,
					// "identifier": k,
				}).Error("approvals.expireEntries: failed to delete expired approval")
				continue
			}

			m.addAuditEntry(approval, types.AuditActionApprovalExpired, "")
		}
	}

	return nil
}

// Subscribe - subscribe for approval events
func (m *DefaultManager) Subscribe(ctx context.Context) (<-chan *types.Approval, error) {
	return m.subscribe(ctx, m.channels), nil
}

// SubscribeApproved - subscribe for approved update requests
func (m *DefaultManager) SubscribeApproved(ctx context.Context) (<-chan *types.Approval, error) {
	return m.subscribe(ctx, m.approvedCh), nil
}

// SubscribeUpdated - subscribe for approvals that changed without a vote
func (m *DefaultManager) SubscribeUpdated(ctx context.Context) (<-chan *types.Approval, error) {
	return m.subscribe(ctx, m.updatedCh), nil
}

func (m *DefaultManager) subscribe(ctx context.Context, channels map[uint32]chan *types.Approval) <-chan *types.Approval {
	m.subMu.Lock()
	index := atomic.AddUint32(&m.index, 1)
	ch := make(chan *types.Approval, 10)
	channels[index] = ch
	m.subMu.Unlock()

	go func() {
		<-ctx.Done()
		m.subMu.Lock()
		delete(channels, index)
		m.subMu.Unlock()
	}()

	return ch
}

func (m *DefaultManager) publishRequest(approval *types.Approval) error {
	return m.publish(m.channels, approval)
}

func (m *DefaultManager) publishApproved(approval *types.Approval) error {
	return m.publish(m.approvedCh, approval)
}

func (m *DefaultManager) publishUpdated(approval *types.Approval) error {
	return m.publish(m.updatedCh, approval)
}

func (m *DefaultManager) publish(channels map[uint32]chan *types.Approval, approval *types.Approval) error {
	m.subMu.RLock()
	defer m.subMu.RUnlock()

	for _, subscriber := range channels {
		subscriber <- approval
	}
	return nil
}

// Update - update approval
func (m *DefaultManager) Update(r *types.Approval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.update(r)
}

func (m *DefaultManager) update(r *types.Approval) error {
	existing, err := m.Get(r.Identifier)
	if err != nil {
		return err
	}

	if r.ID == "" {
		r.ID = existing.ID
	}

	becameApproved := existing.Status() != types.ApprovalStatusApproved && r.Status() == types.ApprovalStatusApproved
	if err = m.store.UpdateApproval(r); err != nil {
		return err
	}

	if becameApproved {
		err = m.publishApproved(r)
		if err != nil {
			log.WithFields(log.Fields{
				"error":    err,
				"approval": r.Identifier,
				"provider": r.Provider,
			}).Error("approvals.manager: failed to re-submit event after approvals were collected")
		}
	}

	return nil
}

// SetRequiredVotes synchronizes active pending approvals with a resource's current requirement.
func (m *DefaultManager) SetRequiredVotes(resourceIdentifier string, provider types.ProviderType, votesRequired int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	activeApprovals, err := m.List()
	if err != nil {
		return err
	}

	prefix := resourceIdentifier + ":"
	for _, approval := range activeApprovals {
		if approval.Provider != provider ||
			!strings.HasPrefix(approval.Identifier, prefix) ||
			approval.Status() != types.ApprovalStatusPending {
			continue
		}

		approval.VotesRequired = votesRequired
		if err := m.update(approval); err != nil {
			return err
		}
	}

	return nil
}

// RequestGroupApproval - request approval for a workload of an approval group. The workloads of a group that
// update to the same change share the approval identified by r.Identifier:
//   - when that approval is active, member joins it, or refreshes its entry when it waits for another image.
//     While the approval is pending its required votes are raised to r.VotesRequired when that is higher;
//     decided approvals keep their votes, so members that arrive late follow the decision.
//   - otherwise r is created with member as its only member, and the other active approvals of the group are
//     archived. Pending ones are marked as superseded by the new change.
//
// A pending approval past its deadline is replaced. A change keyed on a tag rather than a revision is new
// when the member was already deployed or waits for another image. Subscribers are notified of the created
// approval (Subscribe) and of changed approvals (SubscribeUpdated).
func (m *DefaultManager) RequestGroupApproval(r *types.Approval, member types.ApprovalMember) (*types.Approval, error) {
	m.mu.Lock()
	approval, created, updated, err := m.requestGroupApproval(r, member)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// subscribers are notified once the lock is released, processing their queue may need it
	for _, u := range updated {
		m.publishUpdated(u)
	}
	if created {
		m.publishRequest(approval)
	}

	return approval, nil
}

func (m *DefaultManager) requestGroupApproval(r *types.Approval, member types.ApprovalMember) (approval *types.Approval, created bool, updated []*types.Approval, err error) {
	existing, err := m.Get(r.Identifier)
	if err != nil && err != store.ErrRecordNotFound {
		return nil, false, nil, err
	}

	if err == nil && !isStaleGroupApproval(r, existing, member) {
		changed := existing.SetMember(member)
		if existing.Status() == types.ApprovalStatusPending && r.VotesRequired > existing.VotesRequired {
			existing.VotesRequired = r.VotesRequired
			changed = true
		}
		if changed {
			existing.UpdatedAt = time.Now()
			if err := m.store.UpdateApproval(existing); err != nil {
				return nil, false, nil, err
			}
			updated = append(updated, existing)
		}
		return existing, false, updated, nil
	}

	// the members of a group can arrive interleaved across two changes, so a change that was superseded while
	// pending comes back with the members that already joined it
	var revived *types.Approval
	if existing == nil {
		if revived, err = m.supersededGroupApproval(r.Identifier); err != nil {
			return nil, false, nil, err
		}
	}

	active, err := m.List()
	if err != nil {
		return nil, false, nil, err
	}
	for _, other := range active {
		// the approvals list also holds archived approvals, which are settled already
		if other.Group != r.Group || other.Archived {
			continue
		}
		if other.Status() == types.ApprovalStatusPending && !other.Expired() {
			other.SupersededBy = groupChange(r)
			updated = append(updated, other)
		}
		other.Archived = true
		if err := m.store.UpdateApproval(other); err != nil {
			return nil, false, nil, err
		}
		m.addAuditEntry(other, types.AuditActionApprovalArchived, "")
	}

	if revived != nil {
		revived.Archived = false
		revived.SupersededBy = ""
		revived.SetMember(member)
		if r.VotesRequired > revived.VotesRequired {
			revived.VotesRequired = r.VotesRequired
		}
		revived.UpdatedAt = time.Now()
		if err := m.store.UpdateApproval(revived); err != nil {
			return nil, false, nil, err
		}
		return revived, false, append(updated, revived), nil
	}

	r.Members = types.ApprovalMembers{member}
	r.CreatedAt = time.Now()
	r.UpdatedAt = time.Now()

	approval, err = m.store.CreateApproval(r)
	if err != nil {
		return nil, false, nil, fmt.Errorf("failed to create approval: %s", err)
	}

	return approval, true, updated, nil
}

// isStaleGroupApproval - whether the active group approval with the requested identifier can not be joined: it
// is pending past its deadline, or it is keyed on a tag and the member already moved on to another image
func isStaleGroupApproval(r, existing *types.Approval, member types.ApprovalMember) bool {
	if existing.Status() == types.ApprovalStatusPending && existing.Expired() {
		return true
	}
	if r.NewRevision != "" {
		return false
	}
	current := existing.Member(member.Identifier)
	if current == nil {
		return false
	}
	if current.Deployed {
		return true
	}
	return current.Repository.Digest != "" && member.Repository.Digest != "" && current.Repository.Digest != member.Repository.Digest
}

// groupChange - the change a group approval is for: its revision, or else its digest or version
func groupChange(r *types.Approval) string {
	switch {
	case r.NewRevision != "":
		return r.NewRevision
	case r.NewDigest != "":
		return r.NewDigest
	default:
		return r.NewVersion
	}
}

// supersededGroupApproval - the archived approval with the identifier that a newer change superseded while it
// was pending, nil when there is none or when it is past its deadline
func (m *DefaultManager) supersededGroupApproval(identifier string) (*types.Approval, error) {
	archived, err := m.store.ListApprovals(&types.GetApprovalQuery{
		Identifier: identifier,
		Archived:   true,
	})
	if err != nil {
		return nil, err
	}

	for _, approval := range archived {
		if approval.Archived && approval.SupersededBy != "" && approval.Status() == types.ApprovalStatusPending && !approval.Expired() {
			return approval, nil
		}
	}
	return nil, nil
}

// SetGroupMemberDeployed - record that a member of a group approval was updated, so that repeated events for
// the same image do not update it again
func (m *DefaultManager) SetGroupMemberDeployed(identifier, memberIdentifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, err := m.Get(identifier)
	if err != nil {
		return err
	}

	member := existing.Member(memberIdentifier)
	if member == nil || member.Deployed {
		return nil
	}
	member.Deployed = true

	return m.store.UpdateApproval(existing)
}

// Approve - increase VotesReceived by 1 and returns updated version
func (m *DefaultManager) Approve(identifier, voter string) (*types.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, err := m.Get(identifier)
	if err != nil {
		log.WithFields(log.Fields{
			"identifier": identifier,
			"error":      err,
		}).Error("approvals.manager: failed to get")
		return nil, err
	}

	for _, v := range existing.GetVoters() {
		if v == voter {
			// nothing to do, same voter
			return existing, nil
		}
	}

	existing.AddVoter(voter)
	existing.VotesReceived++

	err = m.update(existing)
	if err != nil {
		log.WithFields(log.Fields{
			"identifier": identifier,
			"error":      err,
		}).Error("approvals.manager: failed to update")
		return nil, err
	}

	m.addAuditEntry(existing, types.AuditActionApprovalApproved, voter)

	log.WithFields(log.Fields{
		"identifier": identifier,
	}).Info("approvals.manager: approved")

	return existing, nil
}

func (m *DefaultManager) addAuditEntry(approval *types.Approval, action string, voter string) {

	entry := &types.AuditLog{
		ID:           uuid.New().String(),
		AccountID:    voter,
		Username:     voter,
		Action:       action,
		ResourceKind: types.AuditResourceKindApproval,
		Identifier:   approval.Identifier,
	}

	entry.SetMetadata(map[string]string{
		"provider":        approval.Provider.String(),
		"approval_id":     approval.ID,
		"new_version":     approval.NewVersion,
		"current_version": approval.CurrentVersion,
		"votes_required":  strconv.Itoa(approval.VotesReceived),
		"votes_received":  strconv.Itoa(approval.VotesReceived),
	})

	_, err := m.store.CreateAuditLog(entry)
	if err != nil {
		log.WithFields(log.Fields{
			"error":  err,
			"module": "approvals",
		}).Error("failed to create audit log")
	}
}

// Reject - rejects approval (marks rejected=true), approval will not be valid even if it
// collects required votes
func (m *DefaultManager) Reject(identifier string) (*types.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, err := m.Get(identifier)
	if err != nil {
		return nil, err
	}

	existing.Rejected = true

	err = m.update(existing)
	if err != nil {
		return nil, err
	}

	m.addAuditEntry(existing, types.AuditActionApprovalRejected, "")

	return existing, nil
}

// Get - get specified, not archived approval
func (m *DefaultManager) Get(identifier string) (*types.Approval, error) {

	a, err := m.store.GetApproval(&types.GetApprovalQuery{
		Identifier: identifier,
		Archived:   false,
	})
	if err != nil {
		return nil, err
	}

	// if it's archived, don't display it
	if a.Archived {
		return nil, store.ErrRecordNotFound
	}

	return a, nil
}

// List - list not archived approvals (for expiration service)
func (m *DefaultManager) List() ([]*types.Approval, error) {
	approvals, err := m.store.ListApprovals(&types.GetApprovalQuery{
		Archived: false,
	})
	return approvals, err
}

// Delete - delete specified approval
func (m *DefaultManager) Delete(approval *types.Approval) error {
	existing, err := m.store.GetApproval(&types.GetApprovalQuery{
		ID: approval.ID,
	})
	if err != nil {
		return err
	}

	m.addAuditEntry(existing, types.AuditActionDeleted, "")

	return m.store.DeleteApproval(existing)
}

func (m *DefaultManager) Exists(identifier string) bool {
	_, err := m.Get(identifier)
	if err != nil {
		return false
	}
	return true
}

func (m *DefaultManager) Archive(identifier string) error {
	existing, err := m.Get(identifier)
	if err != nil {
		return fmt.Errorf("approval not found: %s", err)
	}
	existing.Archived = true

	m.addAuditEntry(existing, types.AuditActionApprovalArchived, "")

	return m.store.UpdateApproval(existing)
}

// Create - creates new approval request and publishes to all subscribers
func (m *DefaultManager) Create(r *types.Approval) error {
	_, err := m.Get(r.Identifier)
	if err == nil {
		return ErrApprovalAlreadyExists
	}

	r.CreatedAt = time.Now()
	r.UpdatedAt = time.Now()

	created, err := m.store.CreateApproval(r)
	if err != nil {
		return fmt.Errorf("failed to create approval: %s", err)
	}

	return m.publishRequest(created)
}

func getKey(identifier string) string {
	return ApprovalsPrefix + "/" + identifier
}
