package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/keel-hq/keel/internal/k8s"
	"github.com/keel-hq/keel/types"
	"github.com/keel-hq/keel/util/image"

	v1 "k8s.io/api/core/v1"

	log "github.com/sirupsen/logrus"
)

// rollbackHold - an image that was rolled back on a resource and is not offered again
type rollbackHold struct {
	Digest   string `json:"digest,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// EnableRollbacks makes the provider apply the rollbacks requested through the approvals manager once it starts
func (p *Provider) EnableRollbacks() {
	p.rollbacksEnabled = true
}

// subscribeRollbacks subscribes to rollback requests until the provider stops, when rollbacks are enabled
func (p *Provider) subscribeRollbacks() <-chan *types.Approval {
	if !p.rollbacksEnabled || p.approvalManager == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-p.stop
		cancel()
	}()

	rollbacks, err := p.approvalManager.SubscribeRollback(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Error("provider.kubernetes: failed to subscribe to rollback requests")
		return nil
	}
	return rollbacks
}

// rollback sets every resource updated from the approval back to the image it ran before, pinned by digest. The
// containers keep tracking the reference the update set, and the rolled back image is held so that polling does
// not offer it again. Nothing changes unless every resource can be rolled back.
func (p *Provider) rollback(approval *types.Approval) {
	if approval.Provider != types.ProviderTypeKubernetes {
		return
	}

	resources := make(map[string]*k8s.GenericResource)
	for _, resource := range p.cache.Values() {
		resources[resource.Identifier] = resource
	}

	for _, target := range approval.Rollout {
		resource := resources[target.Identifier]
		switch {
		case resource == nil:
			p.failRollback(approval, fmt.Sprintf("%s not found", target.Name))
			return
		case resource.GetSpecAnnotations()[types.KeelUpdateTimeAnnotation] != target.Marker:
			p.failRollback(approval, fmt.Sprintf("%s was updated again since", target.Name))
			return
		}
	}

	marker := time.Now().String()
	targets := make([]types.RolloutTarget, 0, len(approval.Rollout))
	for _, target := range approval.Rollout {
		targets = append(targets, p.rollbackResource(approval, resources[target.Identifier], target, marker))
	}

	_, err := p.approvalManager.UpdateRollout(approval.ID, func(current *types.Approval) bool {
		for _, target := range targets {
			current.SetRolloutTarget(target)
		}
		return true
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"approval": approval.Identifier,
		}).Error("provider.kubernetes: failed to record the rollback")
		return
	}

	for _, target := range targets {
		if target.State == types.RolloutStateRolling {
			p.watchRollout(approval.ID, target)
		}
	}
}

// rollbackResource pins the containers of the resource to the digests they ran before the update and returns the
// rollout of the rollback
func (p *Provider) rollbackResource(approval *types.Approval, resource *k8s.GenericResource, target types.RolloutTarget, marker string) types.RolloutTarget {
	now := time.Now()
	rollout := types.RolloutTarget{
		Identifier: target.Identifier,
		Name:       target.Name,
		Marker:     marker,
		Containers: target.Containers,
		State:      types.RolloutStateRolling,
		StartedAt:  now,
	}
	fail := func(err error) types.RolloutTarget {
		log.WithFields(log.Fields{
			"error":    err,
			"resource": target.Identifier,
		}).Error("provider.kubernetes: failed to roll back resource")
		rollout.State = types.RolloutStateFailed
		rollout.Reason = "rollback failed: " + err.Error()
		rollout.FinishedAt = time.Now()
		return rollout
	}

	annotations := resource.GetAnnotations()
	tracked := trackedImages(annotations)
	holds := rollbackHolds(annotations)

	for _, container := range target.Containers {
		pinned, err := pinImage(container.Image, container.PreviousDigest)
		if err != nil {
			return fail(err)
		}
		if !setContainerImage(resource, container.Name, pinned) {
			return fail(fmt.Errorf("container %s not found", container.Name))
		}

		tracked[container.Name] = container.Image
		if ref, err := image.Parse(container.Image); err == nil {
			holds[ref.Repository()] = rollbackHold{Digest: container.NewDigest, Revision: approval.NewRevision}
		}
		annotations[types.KeelDigestAnnotation] = container.PreviousDigest
	}

	setJSONAnnotation(annotations, types.KeelTrackedImagesAnnotation, tracked, len(tracked))
	setJSONAnnotation(annotations, types.KeelRollbackHoldAnnotation, holds, len(holds))
	annotations["kubernetes.io/change-cause"] = fmt.Sprintf("keel rollback of %s by %s [%s]", approval.Identifier, approval.RolledBackBy, now.Format(time.RFC3339))
	resource.SetAnnotations(annotations)

	specAnnotations := resource.GetSpecAnnotations()
	specAnnotations[types.KeelUpdateTimeAnnotation] = marker
	resource.SetSpecAnnotations(specAnnotations)

	if err := p.implementer.Update(resource); err != nil {
		return fail(err)
	}

	log.WithFields(log.Fields{
		"name":        resource.Name,
		"namespace":   resource.Namespace,
		"approval":    approval.Identifier,
		"rolled_back": approval.RolledBackBy,
	}).Info("provider.kubernetes: resource rolled back")

	return rollout
}

// failRollback records why a requested rollback was not applied, so that the approval shows it
func (p *Provider) failRollback(approval *types.Approval, reason string) {
	log.WithFields(log.Fields{
		"approval": approval.Identifier,
		"reason":   reason,
	}).Warn("provider.kubernetes: rollback not applied")

	_, err := p.approvalManager.UpdateRollout(approval.ID, func(current *types.Approval) bool {
		current.RolledBackBy = ""
		current.RolledBackAt = nil
		current.RollbackFailure = reason
		return true
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"approval": approval.Identifier,
		}).Error("provider.kubernetes: failed to record the refused rollback")
	}
}

// matchingContainers returns copies of the tracked containers of the resource whose image is in the repository of
// the event, taken before an update changes their images
func matchingContainers(resource *k8s.GenericResource, repo *types.Repository) []v1.Container {
	eventRef, err := image.Parse(repo.String())
	if err != nil {
		return nil
	}

	labels := resource.GetLabels()
	annotations := resource.GetAnnotations()
	filter := GetMonitorContainersFromMeta(labels, annotations)

	candidates := append([]v1.Container(nil), resource.Containers()...)
	if annotations[types.KeelInitContainerAnnotation] == "true" {
		candidates = append(candidates, resource.InitContainers()...)
	}

	var matching []v1.Container
	for _, container := range candidates {
		if !filter(container) {
			continue
		}
		ref, err := image.Parse(trackedContainerImage(resource, container))
		if err != nil || ref.Repository() != eventRef.Repository() {
			continue
		}
		matching = append(matching, container)
	}
	return matching
}

// recordUpdatedContainers records the containers a plan updates with the images it sets, and the digest a
// container pinned by digest ran. The candidates are the matching containers as they were before the update.
func recordUpdatedContainers(plan *UpdatePlan, candidates []v1.Container) {
	updated := append(plan.Resource.Containers(), plan.Resource.InitContainers()...)
	for _, candidate := range candidates {
		updatedImage := candidate.Image
		for _, container := range updated {
			if container.Name == candidate.Name {
				updatedImage = container.Image
				break
			}
		}
		plan.containers = append(plan.containers, types.RolloutContainer{Name: candidate.Name, Image: updatedImage})
		if digest := pinnedDigest(candidate.Image); digest != "" {
			plan.pinnedDigest = digest
		}
	}
}

// trackedContainerImage returns the image Keel tracks for a container: a container pinned by a rollback keeps
// tracking the reference its update set (keel.sh/trackedImages), other containers track their own image
func trackedContainerImage(resource *k8s.GenericResource, container v1.Container) string {
	if pinnedDigest(container.Image) == "" {
		return container.Image
	}
	if tracked := trackedImages(resource.GetAnnotations())[container.Name]; tracked != "" {
		return tracked
	}
	return container.Image
}

// trackedImageReferences maps the images of the containers pinned by a rollback to the images they keep tracking
func trackedImageReferences(resource *k8s.GenericResource) map[string]string {
	tracked := trackedImages(resource.GetAnnotations())
	if len(tracked) == 0 {
		return nil
	}
	references := make(map[string]string)
	for _, container := range append(resource.Containers(), resource.InitContainers()...) {
		if reference := tracked[container.Name]; reference != "" && pinnedDigest(container.Image) != "" {
			references[container.Image] = reference
		}
	}
	return references
}

// isHeldImage reports whether the image of the event was rolled back on the resource, which then does not offer
// it again
func isHeldImage(resource *k8s.GenericResource, repo *types.Repository) bool {
	hold, ok := heldImage(resource, repo)
	return ok && repo.Digest != "" && hold.Digest == repo.Digest
}

// isHeldRevision reports whether the revision of the image repository was rolled back on the resource
func isHeldRevision(resource *k8s.GenericResource, repo *types.Repository, revision string) bool {
	hold, ok := heldImage(resource, repo)
	return ok && revision != "" && hold.Revision == revision
}

func heldImage(resource *k8s.GenericResource, repo *types.Repository) (rollbackHold, bool) {
	holds := rollbackHolds(resource.GetAnnotations())
	if len(holds) == 0 {
		return rollbackHold{}, false
	}
	ref, err := image.Parse(repo.String())
	if err != nil {
		return rollbackHold{}, false
	}
	hold, ok := holds[ref.Repository()]
	return hold, ok
}

// releaseRollback clears the tracked images and the holds of the containers an update sets back to the reference
// they track
func releaseRollback(annotations map[string]string, containers []types.RolloutContainer) {
	tracked := trackedImages(annotations)
	holds := rollbackHolds(annotations)
	if len(tracked) == 0 && len(holds) == 0 {
		return
	}
	for _, container := range containers {
		delete(tracked, container.Name)
		if ref, err := image.Parse(container.Image); err == nil {
			delete(holds, ref.Repository())
		}
	}
	setJSONAnnotation(annotations, types.KeelTrackedImagesAnnotation, tracked, len(tracked))
	setJSONAnnotation(annotations, types.KeelRollbackHoldAnnotation, holds, len(holds))
}

func trackedImages(annotations map[string]string) map[string]string {
	tracked := make(map[string]string)
	if value := annotations[types.KeelTrackedImagesAnnotation]; value != "" {
		if err := json.Unmarshal([]byte(value), &tracked); err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"value": value,
			}).Warn("provider.kubernetes: ignoring invalid tracked images annotation")
			return make(map[string]string)
		}
	}
	return tracked
}

func rollbackHolds(annotations map[string]string) map[string]rollbackHold {
	holds := make(map[string]rollbackHold)
	if value := annotations[types.KeelRollbackHoldAnnotation]; value != "" {
		if err := json.Unmarshal([]byte(value), &holds); err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"value": value,
			}).Warn("provider.kubernetes: ignoring invalid rollback hold annotation")
			return make(map[string]rollbackHold)
		}
	}
	return holds
}

// setJSONAnnotation stores the value as JSON, or removes the annotation when the value has no entries
func setJSONAnnotation(annotations map[string]string, key string, value interface{}, entries int) {
	if entries == 0 {
		delete(annotations, key)
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	annotations[key] = string(encoded)
}

// pinnedDigest returns the digest of an image reference pinned by digest, ie: registry.example.com/app@sha256:...
func pinnedDigest(reference string) string {
	if i := strings.LastIndex(reference, "@"); i != -1 {
		return reference[i+1:]
	}
	return ""
}

// pinImage returns the reference of the image pinned to the digest, ie: registry.example.com/app:main ->
// registry.example.com/app@sha256:...
func pinImage(reference, digest string) (string, error) {
	ref, err := image.Parse(reference)
	if err != nil {
		return "", err
	}
	if ref.Registry() == image.DefaultRegistryHostname {
		return ref.ShortName() + "@" + digest, nil
	}
	return ref.Repository() + "@" + digest, nil
}

// setContainerImage sets the image of the container or init container with the name
func setContainerImage(resource *k8s.GenericResource, name, reference string) bool {
	for idx, container := range resource.Containers() {
		if container.Name == name {
			resource.UpdateContainer(idx, reference)
			return true
		}
	}
	for idx, container := range resource.InitContainers() {
		if container.Name == name {
			resource.UpdateInitContainer(idx, reference)
			return true
		}
	}
	return false
}
