package kubernetes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/keel-hq/keel/internal/k8s"
	"github.com/keel-hq/keel/types"

	apps_v1 "k8s.io/api/apps/v1"
)

func rollbackDeployment(name, previousDigest string) *apps_v1.Deployment {
	deployment := groupDeployment(name, "trackeid", "1")
	deployment.Annotations[types.KeelDigestAnnotation] = previousDigest
	deployment.Spec.Template.Spec.Containers[0].Name = "app"
	return deployment
}

// syncUpdates makes the cache show the resources the provider updated, as the cluster watch would, and returns
// them by name
func (f *groupFixture) syncUpdates() map[string]*k8s.GenericResource {
	f.t.Helper()
	fp := f.provider.implementer.(*fakeImplementer)
	fp.mu.Lock()
	updates := fp.updates
	fp.updates = nil
	fp.mu.Unlock()

	cache := f.provider.cache.(*k8s.GenericResourceCache)
	byName := make(map[string]*k8s.GenericResource)
	for _, resource := range updates {
		cache.Add(resource.DeepCopy())
		byName[resource.Name] = resource
	}
	return byName
}

func (f *groupFixture) approvalByID(id string) *types.Approval {
	f.t.Helper()
	approval, err := f.provider.approvalManager.UpdateRollout(id, func(*types.Approval) bool { return false })
	if err != nil {
		f.t.Fatalf("failed to get approval %s: %s", id, err)
	}
	return approval
}

// deployRevisionA deploys revision A of the api and the portal with a group approval
func deployRevisionA(t *testing.T) (*groupFixture, map[string]string) {
	previous := map[string]string{"trackeid-api": groupDigest("1"), "trackeid-portal": groupDigest("2")}
	f := newGroupFixture(t, rollbackDeployment("trackeid-api", previous["trackeid-api"]), rollbackDeployment("trackeid-portal", previous["trackeid-portal"]))
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)
	if updated := f.approve("group/trackeid/trackeid:" + groupRevisionA); strings.Join(updated, ",") != "trackeid-api,trackeid-portal" {
		t.Fatalf("expected both members to be deployed, got %v", updated)
	}
	f.syncUpdates()
	return f, previous
}

func annotationMap(t *testing.T, resource *k8s.GenericResource, key string) map[string]interface{} {
	t.Helper()
	values := map[string]interface{}{}
	if value := resource.GetAnnotations()[key]; value != "" {
		if err := json.Unmarshal([]byte(value), &values); err != nil {
			t.Fatalf("invalid %s annotation %q: %s", key, value, err)
		}
	}
	return values
}

func TestRollbackPinsPreviousImagesOfAllMembers(t *testing.T) {
	f, previous := deployRevisionA(t)

	requested, err := f.provider.approvalManager.RequestRollback("group/trackeid/trackeid:"+groupRevisionA, "U02ROLLBACK")
	if err != nil {
		t.Fatalf("failed to request the rollback: %s", err)
	}
	f.provider.rollback(requested)

	updates := f.syncUpdates()
	if len(updates) != 2 {
		t.Fatalf("expected both members to be rolled back, got %d updates", len(updates))
	}
	for name, digest := range previous {
		resource := updates[name]
		if got := resource.Containers()[0].Image; got != "registry.example.com/tr8s/"+name+"@"+digest {
			t.Errorf("expected %s to be pinned to its previous digest, got %s", name, got)
		}
		if tracked := annotationMap(t, resource, types.KeelTrackedImagesAnnotation); tracked["app"] != "registry.example.com/tr8s/"+name+":main" {
			t.Errorf("expected %s to keep tracking the tag, got %v", name, tracked)
		}
		if holds := annotationMap(t, resource, types.KeelRollbackHoldAnnotation); holds["registry.example.com/tr8s/"+name] == nil {
			t.Errorf("expected %s to hold the rolled back image, got %v", name, holds)
		}
	}

	approval := f.approvalByID(requested.ID)
	if approval.RolledBackBy != "U02ROLLBACK" || approval.RolledBackAt == nil || approval.RolloutState() != types.RolloutStateRolling {
		t.Errorf("expected the rollback by U02ROLLBACK to roll out, got %q %v %q", approval.RolledBackBy, approval.RolledBackAt, approval.RolloutState())
	}
}

func TestRollbackHoldsRolledBackRevision(t *testing.T) {
	f, _ := deployRevisionA(t)
	requested, err := f.provider.approvalManager.RequestRollback("group/trackeid/trackeid:"+groupRevisionA, "U02ROLLBACK")
	if err != nil {
		t.Fatalf("failed to request the rollback: %s", err)
	}
	f.provider.rollback(requested)
	f.syncUpdates()

	// polling keeps watching the tag of the pinned containers
	tracked, err := f.provider.TrackedImages()
	if err != nil {
		t.Fatalf("failed to get tracked images: %s", err)
	}
	for _, image := range tracked {
		if image.Image.Tag() != "main" {
			t.Errorf("expected the tag to stay tracked, got %s", image.Image.Remote())
		}
	}

	// neither the rolled back image nor a rebuild of the rolled back revision is offered again
	if updated := f.push("trackeid-api", groupDigest("a"), groupRevisionA); len(updated) != 0 {
		t.Errorf("expected the rolled back image to be held, got %v", updated)
	}
	if updated := f.push("trackeid-api", groupDigest("e"), groupRevisionA); len(updated) != 0 {
		t.Errorf("expected the rolled back revision to be held, got %v", updated)
	}
	if approvals := f.groupApprovals(); len(approvals) != 1 || approvals[0].Identifier != "group/trackeid/trackeid:"+groupRevisionA {
		t.Fatalf("expected no new approval for the held revision, got %d approvals", len(approvals))
	}

	// a new revision asks for approval as usual and sets the tag back once approved
	if updated := f.push("trackeid-api", groupDigest("c"), groupRevisionB); len(updated) != 0 {
		t.Fatalf("expected the new revision to wait for approval, got %v", updated)
	}
	pending := f.onlyGroupApproval()
	if pending.Identifier != "group/trackeid/trackeid:"+groupRevisionB || pending.Status() != types.ApprovalStatusPending {
		t.Fatalf("expected a pending approval for the new revision, got %s", pending.Identifier)
	}
	if updated := f.approve(pending.Identifier); strings.Join(updated, ",") != "trackeid-api" {
		t.Fatalf("expected the api to be updated, got %v", updated)
	}
	api := f.syncUpdates()["trackeid-api"]
	if got := api.Containers()[0].Image; got != "registry.example.com/tr8s/trackeid-api:main" {
		t.Errorf("expected the api to track the tag again, got %s", got)
	}
	for _, key := range []string{types.KeelTrackedImagesAnnotation, types.KeelRollbackHoldAnnotation} {
		if value, ok := api.GetAnnotations()[key]; ok {
			t.Errorf("expected %s to be cleared, got %s", key, value)
		}
	}
	if target := f.approvalByID(f.onlyGroupApproval().ID).RolloutTarget("deployment/trackeid/trackeid-api"); target == nil || target.Containers[0].PreviousDigest != groupDigest("1") {
		t.Errorf("expected the pinned digest to be recorded as the previous image, got %+v", target)
	}
}

func TestRollbackRefusedWhenResourceWasUpdatedAgain(t *testing.T) {
	f, _ := deployRevisionA(t)

	cache := f.provider.cache.(*k8s.GenericResourceCache)
	for _, resource := range cache.Values() {
		if resource.Name == "trackeid-api" {
			annotations := resource.GetSpecAnnotations()
			annotations[types.KeelUpdateTimeAnnotation] = "a newer update"
			resource.SetSpecAnnotations(annotations)
			cache.Add(resource)
		}
	}

	requested, err := f.provider.approvalManager.RequestRollback("group/trackeid/trackeid:"+groupRevisionA, "U02ROLLBACK")
	if err != nil {
		t.Fatalf("failed to request the rollback: %s", err)
	}
	f.provider.rollback(requested)

	if updates := f.syncUpdates(); len(updates) != 0 {
		t.Errorf("expected nothing to be rolled back, got %d updates", len(updates))
	}
	approval := f.approvalByID(requested.ID)
	if approval.RollbackFailure != "trackeid-api was updated again since" || approval.RolledBackBy != "" {
		t.Errorf("expected the refused rollback to be recorded, got %q by %q", approval.RollbackFailure, approval.RolledBackBy)
	}
}
