package kubernetes

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/keel-hq/keel/internal/k8s"
	keelprovider "github.com/keel-hq/keel/provider"
	"github.com/keel-hq/keel/types"

	apps_v1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	groupRevisionA = "8f714c1a2b3c4d5e6f708192a3b4c5d6e7f80910"
	groupRevisionB = "9c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5"
)

func groupDigest(c string) string {
	return "sha256:" + strings.Repeat(c, 64)
}

func groupDeployment(name, group, approvals string) *apps_v1.Deployment {
	annotations := map[string]string{types.KeelMinimumApprovalsLabel: approvals}
	if group != "" {
		annotations[types.KeelApprovalGroupAnnotation] = group
	}
	return &apps_v1.Deployment{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:        name,
			Namespace:   "trackeid",
			Labels:      map[string]string{types.KeelPolicyLabel: "force", types.KeelForceTagMatchLabel: "true"},
			Annotations: annotations,
		},
		Spec: apps_v1.DeploymentSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{{Image: "registry.example.com/tr8s/" + name + ":main"}},
				},
			},
		},
	}
}

type groupFixture struct {
	t        *testing.T
	provider *Provider
	labels   *fakeImageLabels
	updates  <-chan *types.Approval
}

func newGroupFixture(t *testing.T, deployments ...*apps_v1.Deployment) *groupFixture {
	t.Helper()

	fp := &fakeImplementer{
		namespaces: &v1.NamespaceList{Items: []v1.Namespace{{ObjectMeta: meta_v1.ObjectMeta{Name: "trackeid"}}}},
	}
	grc := &k8s.GenericResourceCache{}
	grc.Add(MustParseGRS(deployments)...)

	approver, teardown := approver()
	t.Cleanup(teardown)

	provider, err := NewProvider(fp, &fakeSender{}, approver, grc)
	if err != nil {
		t.Fatalf("failed to get provider: %s", err)
	}
	labels := &fakeImageLabels{labels: map[string]map[string]string{}}
	provider.SetImageLabelsGetter(labels)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	updates, err := approver.SubscribeUpdated(ctx)
	if err != nil {
		t.Fatalf("failed to subscribe to updates: %s", err)
	}

	return &groupFixture{t: t, provider: provider, labels: labels, updates: updates}
}

// push processes a poll event for a new image of the workload, labelled with the revision when not empty, and
// returns the updated workloads
func (f *groupFixture) push(name, digest, revision string) []string {
	f.t.Helper()
	if revision != "" {
		f.labels.labels[digest] = map[string]string{
			types.OCIImageSourceLabel:   "https://github.com/tr8s/trackeid",
			types.OCIImageRevisionLabel: revision,
		}
	}
	return f.process(types.Event{Repository: types.Repository{
		Name:   "registry.example.com/tr8s/" + name,
		Tag:    "main",
		Digest: digest,
	}})
}

func (f *groupFixture) process(event types.Event) []string {
	f.t.Helper()
	updated, err := f.provider.processEvent(&event)
	if err != nil {
		f.t.Fatalf("failed to process event: %s", err)
	}
	var names []string
	for _, resource := range updated {
		names = append(names, resource.Name)
	}
	sort.Strings(names)
	return names
}

// approve approves the approval and processes the events that the providers submit once it is approved
func (f *groupFixture) approve(identifier string) []string {
	f.t.Helper()
	approval, err := f.provider.approvalManager.Approve(identifier, "U01ABCDEF")
	if err != nil {
		f.t.Fatalf("failed to approve: %s", err)
	}
	var names []string
	for _, event := range keelprovider.ApprovedEvents(approval) {
		names = append(names, f.process(event)...)
	}
	sort.Strings(names)
	return names
}

// groupApprovals returns the active approvals of the trackeid approval group
func (f *groupFixture) groupApprovals() []*types.Approval {
	f.t.Helper()
	approvals, err := f.provider.approvalManager.List()
	if err != nil {
		f.t.Fatalf("failed to list approvals: %s", err)
	}
	var group []*types.Approval
	for _, approval := range approvals {
		// the approvals list also holds archived approvals
		if approval.Group == "trackeid/trackeid" && !approval.Archived {
			group = append(group, approval)
		}
	}
	return group
}

func (f *groupFixture) onlyGroupApproval() *types.Approval {
	f.t.Helper()
	approvals := f.groupApprovals()
	if len(approvals) != 1 {
		f.t.Fatalf("expected one active group approval, got %d: %+v", len(approvals), approvals)
	}
	return approvals[0]
}

func (f *groupFixture) nextUpdate() *types.Approval {
	f.t.Helper()
	select {
	case approval := <-f.updates:
		return approval
	case <-time.After(time.Second):
		f.t.Fatal("expected an approval update to be published")
		return nil
	}
}

func memberIdentifiers(approval *types.Approval) string {
	var identifiers []string
	for _, member := range approval.Members {
		identifiers = append(identifiers, member.Identifier)
	}
	return strings.Join(identifiers, ",")
}

func TestGroupApprovalSharedByMembers(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))

	if updated := f.push("trackeid-api", groupDigest("a"), groupRevisionA); len(updated) != 0 {
		t.Fatalf("expected no update before approval, got %v", updated)
	}
	if updated := f.push("trackeid-portal", groupDigest("b"), groupRevisionA); len(updated) != 0 {
		t.Fatalf("expected no update before approval, got %v", updated)
	}

	approval := f.onlyGroupApproval()
	if approval.Identifier != "group/trackeid/trackeid:"+groupRevisionA {
		t.Errorf("unexpected identifier: %s", approval.Identifier)
	}
	if got := memberIdentifiers(approval); got != "deployment/trackeid/trackeid-api,deployment/trackeid/trackeid-portal" {
		t.Errorf("unexpected members: %s", got)
	}
	if approval.NewRevision != groupRevisionA || approval.Status() != types.ApprovalStatusPending {
		t.Errorf("unexpected approval: %+v", approval)
	}
	for _, identifier := range []string{"deployment/trackeid/trackeid-api:main", "deployment/trackeid/trackeid-portal:main"} {
		if f.provider.approvalManager.Exists(identifier) {
			t.Errorf("did not expect a per-resource approval %s", identifier)
		}
	}
}

func TestGroupApprovalLateMemberJoinsPending(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))

	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	if got := memberIdentifiers(f.onlyGroupApproval()); got != "deployment/trackeid/trackeid-api" {
		t.Fatalf("unexpected members: %s", got)
	}

	if updated := f.push("trackeid-portal", groupDigest("b"), groupRevisionA); len(updated) != 0 {
		t.Fatalf("expected no update while pending, got %v", updated)
	}

	update := f.nextUpdate()
	if update.Identifier != "group/trackeid/trackeid:"+groupRevisionA || len(update.Members) != 2 || update.Status() != types.ApprovalStatusPending {
		t.Errorf("expected the pending approval to be republished with both members, got %+v", update)
	}
}

func TestGroupApprovalApproveDeploysAllMembers(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)

	identifier := "group/trackeid/trackeid:" + groupRevisionA
	if updated := f.approve(identifier); strings.Join(updated, ",") != "trackeid-api,trackeid-portal" {
		t.Fatalf("expected both members to be updated, got %v", updated)
	}

	approval := f.onlyGroupApproval()
	for _, member := range approval.Members {
		if !member.Deployed {
			t.Errorf("expected member %s to be recorded as deployed", member.Identifier)
		}
	}

	// the same image again neither updates twice nor asks again
	if updated := f.push("trackeid-api", groupDigest("a"), groupRevisionA); len(updated) != 0 {
		t.Errorf("expected no second update, got %v", updated)
	}
	if got := f.onlyGroupApproval().Identifier; got != identifier {
		t.Errorf("unexpected approval: %s", got)
	}
}

func TestGroupApprovalLateMemberAfterApprovalDeploys(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)

	identifier := "group/trackeid/trackeid:" + groupRevisionA
	if updated := f.approve(identifier); strings.Join(updated, ",") != "trackeid-api" {
		t.Fatalf("expected the api to be updated, got %v", updated)
	}

	if updated := f.push("trackeid-portal", groupDigest("b"), groupRevisionA); strings.Join(updated, ",") != "trackeid-portal" {
		t.Fatalf("expected the late member to be updated without asking, got %v", updated)
	}

	approval := f.onlyGroupApproval()
	if approval.Identifier != identifier || len(approval.Members) != 2 || !approval.Member("deployment/trackeid/trackeid-portal").Deployed {
		t.Errorf("expected the late member to join the approved approval, got %+v", approval)
	}
}

func TestGroupApprovalLateMemberAfterRejectionDoesNotDeploy(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)

	identifier := "group/trackeid/trackeid:" + groupRevisionA
	if _, err := f.provider.approvalManager.Reject(identifier); err != nil {
		t.Fatalf("failed to reject: %s", err)
	}

	if updated := f.push("trackeid-portal", groupDigest("b"), groupRevisionA); len(updated) != 0 {
		t.Fatalf("expected the late member not to be updated, got %v", updated)
	}

	approval := f.onlyGroupApproval()
	if approval.Identifier != identifier || !approval.Rejected {
		t.Errorf("expected the rejected approval to remain the only one, got %+v", approval)
	}
}

func TestGroupApprovalNewRevisionSupersedesPending(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-api", groupDigest("c"), groupRevisionB)

	if got := f.onlyGroupApproval().Identifier; got != "group/trackeid/trackeid:"+groupRevisionB {
		t.Fatalf("expected the new revision to have its own approval, got %s", got)
	}

	superseded := f.nextUpdate()
	if superseded.Identifier != "group/trackeid/trackeid:"+groupRevisionA || superseded.SupersededBy != groupRevisionB || !superseded.Archived {
		t.Errorf("expected the pending approval to be superseded, got %+v", superseded)
	}
	if _, err := f.provider.approvalManager.Approve("group/trackeid/trackeid:"+groupRevisionA, "U01ABCDEF"); err == nil {
		t.Error("expected the superseded approval not to accept votes")
	}
}

func TestGroupApprovalMovingTagWithoutRevision(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))

	// without revision labels the approval is keyed on the tag
	f.push("trackeid-api", groupDigest("a"), "")
	if got := f.onlyGroupApproval().Identifier; got != "group/trackeid/trackeid:main" {
		t.Fatalf("unexpected identifier: %s", got)
	}

	// a new image behind the tag is a new change
	f.push("trackeid-api", groupDigest("c"), "")
	approval := f.onlyGroupApproval()
	if got := approval.Member("deployment/trackeid/trackeid-api").Repository.Digest; got != groupDigest("c") {
		t.Errorf("expected the approval for the new image, got digest %s", got)
	}
	if superseded := f.nextUpdate(); superseded.SupersededBy != groupDigest("c") || superseded.Member("deployment/trackeid/trackeid-api").Repository.Digest != groupDigest("a") {
		t.Errorf("expected the approval for the first image to be superseded, got %+v", superseded)
	}
}

func TestGroupApprovalVotesRequiredIsHighest(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "3"))

	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	first := f.onlyGroupApproval()
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)

	approval := f.onlyGroupApproval()
	if approval.VotesRequired != 3 {
		t.Errorf("expected 3 required votes, got %d", approval.VotesRequired)
	}
	if !approval.Deadline.Equal(first.Deadline) {
		t.Errorf("expected the deadline of the first member %s, got %s", first.Deadline, approval.Deadline)
	}
}

func TestApprovalWithoutGroupKeepsPerResourceApprovals(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "", "1"), groupDeployment("trackeid-portal", "", "1"))
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)

	for _, identifier := range []string{"deployment/trackeid/trackeid-api:main", "deployment/trackeid/trackeid-portal:main"} {
		if !f.provider.approvalManager.Exists(identifier) {
			t.Errorf("expected per-resource approval %s", identifier)
		}
	}
	if approvals := f.groupApprovals(); len(approvals) != 0 {
		t.Errorf("did not expect group approvals, got %+v", approvals)
	}
}

func TestGroupApprovalInterleavedChangesKeepMembers(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))

	// the api reaches the second change before the portal reaches the first one
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-api", groupDigest("c"), groupRevisionB)
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)
	f.push("trackeid-portal", groupDigest("d"), groupRevisionB)

	approval := f.onlyGroupApproval()
	if approval.Identifier != "group/trackeid/trackeid:"+groupRevisionB || memberIdentifiers(approval) != "deployment/trackeid/trackeid-api,deployment/trackeid/trackeid-portal" {
		t.Fatalf("expected the second change with both members, got %s with %s", approval.Identifier, memberIdentifiers(approval))
	}
	if updated := f.approve(approval.Identifier); strings.Join(updated, ",") != "trackeid-api,trackeid-portal" {
		t.Errorf("expected both members to be updated, got %v", updated)
	}
}
