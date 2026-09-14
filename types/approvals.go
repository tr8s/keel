package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type GetApprovalQuery struct {
	ID         string
	Identifier string
	// Rejected   bool
	Archived bool
}

// Approval used to store and track updates
type Approval struct {
	ID string `json:"id" gorm:"primary_key;type:varchar(36)"`

	// Archived is set to true once approval is finally approved/rejected
	Archived bool `json:"archived"`

	// Provider name - Kubernetes/Helm
	Provider ProviderType `json:"provider"`

	// Identifier is used to inform user about specific
	// Helm release or k8s deployment
	// ie: k8s <namespace>/<deployment name>
	//     helm: <namespace>/<release name>
	Identifier string `json:"identifier"`

	// Event that triggered evaluation
	Event *Event `json:"event" gorm:"type:json"`

	Message string `json:"message"`

	CurrentVersion string `json:"currentVersion"`
	NewVersion     string `json:"newVersion"`

	// CurrentDigest and NewDigest identify the images behind CurrentVersion
	// and NewVersion when known, so that updates keeping the same tag
	// (ie: main -> main) can be told apart.
	CurrentDigest string `json:"currentDigest,omitempty"`
	NewDigest     string `json:"newDigest,omitempty"`

	// SourceURL, CurrentRevision and NewRevision describe the code change
	// behind the update when the images carry the
	// org.opencontainers.image.source and org.opencontainers.image.revision
	// labels. Empty when unknown.
	SourceURL       string `json:"sourceUrl,omitempty"`
	CurrentRevision string `json:"currentRevision,omitempty"`
	NewRevision     string `json:"newRevision,omitempty"`

	// CommitSubject and CommitAuthor describe the commit the new image was
	// built from when it carries the sh.keel.commit.subject and
	// sh.keel.commit.author labels. Empty when unknown.
	CommitSubject string `json:"commitSubject,omitempty"`
	CommitAuthor  string `json:"commitAuthor,omitempty"`

	// Group, Members and SupersededBy are set on the approval shared by the
	// workloads of an approval group (keel.sh/approvalGroup) that update to
	// the same change. Group is "<namespace>/<group>", Members are the
	// workloads the approval decides for, and SupersededBy is the revision
	// (or else digest or version) of the newer change that replaced the
	// approval while it was pending.
	Group        string          `json:"group,omitempty"`
	Members      ApprovalMembers `json:"members,omitempty" gorm:"type:json"`
	SupersededBy string          `json:"supersededBy,omitempty"`

	// Rollout follows the rollout of every resource updated from the approval
	Rollout RolloutTargets `json:"rollout,omitempty" gorm:"type:json"`

	// MessageChannel and MessageTimestamp identify the message a bot posted
	// for the approval, so that it can update the message and reply in its
	// thread. Empty when the bot does not record them.
	MessageChannel   string `json:"messageChannel,omitempty"`
	MessageTimestamp string `json:"messageTimestamp,omitempty"`

	// Digest is used to verify that images are the ones that got the approvals.
	// If digest doesn't match for the image, votes are reset.
	Digest string `json:"digest"`

	// Requirements for the update such as number of votes
	// and deadline
	VotesRequired int `json:"votesRequired"`
	VotesReceived int `json:"votesReceived"`

	// Voters is a list of voter
	// IDs for audit
	Voters JSONB `json:"voters" gorm:"type:json"`

	// Explicitly rejected approval
	// can be set directly by user
	// so even if deadline is not reached approval
	// could be turned down
	Rejected bool `json:"rejected"`

	// Deadline for this request
	Deadline time.Time `json:"deadline"`

	// When this approval was created
	CreatedAt time.Time `json:"createdAt"`
	// WHen this approval was updated
	UpdatedAt time.Time `json:"updatedAt"`
}

func (a *Approval) GetVoters() []string {
	// meta := make(map[string]string)
	var voters []string
	for key := range a.Voters {
		voters = append(voters, key)

	}
	return voters
}

func (a *Approval) AddVoter(voter string) {
	if a.Voters == nil {
		a.Voters = make(map[string]interface{})
	}
	a.Voters[voter] = time.Now()
}

// ApprovalStatus - approval status type used in approvals
// to determine whether it was rejected/approved or still pending
type ApprovalStatus int

// Available approval status types
const (
	ApprovalStatusUnknown ApprovalStatus = iota
	ApprovalStatusPending
	ApprovalStatusApproved
	ApprovalStatusRejected
)

func (s ApprovalStatus) String() string {
	switch s {
	case ApprovalStatusPending:
		return "pending"
	case ApprovalStatusApproved:
		return "approved"
	case ApprovalStatusRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// Status - returns current approval status
func (a *Approval) Status() ApprovalStatus {
	if a.Rejected {
		return ApprovalStatusRejected
	}

	if a.VotesReceived >= a.VotesRequired {
		return ApprovalStatusApproved
	}

	return ApprovalStatusPending
}

// Expired - checks if approval is already expired
func (a *Approval) Expired() bool {
	return a.Deadline.Before(time.Now())
}

// Delta of what's changed
// ie: webhookrelay/webhook-demo:0.15.0 -> webhookrelay/webhook-demo:0.16.0
// When the tag stays the same, the known image digests tell the images apart
// ie: main@sha256:5f55a51b -> main@sha256:62c200e9
func (a *Approval) Delta() string {
	current, next := a.CurrentVersion, a.NewVersion
	if current == next {
		current = versionWithDigest(current, a.CurrentDigest)
		next = versionWithDigest(next, a.NewDigest)
	}
	return fmt.Sprintf("%s -> %s", current, next)
}

// ShortDigest abbreviates a digest for display, ie: sha256:62c200e9
func ShortDigest(digest string) string {
	if algorithm, hex, ok := strings.Cut(digest, ":"); ok && len(hex) > 8 {
		return algorithm + ":" + hex[:8]
	}
	return digest
}

// versionWithDigest appends an abbreviated digest to a version when the
// digest is known, ie: main@sha256:62c200e9
func versionWithDigest(version, digest string) string {
	if digest == "" {
		return version
	}
	return version + "@" + ShortDigest(digest)
}

// JSONB is stored as a JSON blob
type JSONB map[string]interface{}

func (b JSONB) Value() (driver.Value, error) {
	j, err := json.Marshal(b)
	return j, err
}

func (b *JSONB) Scan(src interface{}) error {
	source, ok := src.([]byte)
	if !ok {
		return errors.New("type assertion .([]byte) failed.")
	}

	var i interface{}
	if err := json.Unmarshal(source, &i); err != nil {
		return err
	}

	if i == nil {
		return nil
	}

	*b, ok = i.(map[string]interface{})
	if !ok {
		return errors.New("type assertion .(map[string]interface{}) failed.")
	}

	return nil
}
