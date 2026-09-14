package kubernetes

import (
	"sync"
	"time"

	"github.com/keel-hq/keel/internal/k8s"
	"github.com/keel-hq/keel/pkg/store"
	"github.com/keel-hq/keel/types"

	apps_v1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"

	log "github.com/sirupsen/logrus"
)

// DefaultRolloutTimeout - how long the rollout of an approved update may take before it is reported as failed,
// when the workload does not report the failure itself (ie: with progressDeadlineSeconds)
const DefaultRolloutTimeout = 10 * time.Minute

// defaultRolloutInterval - how often the rollouts in progress are checked
const defaultRolloutInterval = 5 * time.Second

// rolloutWatcher follows the rollouts of resources updated from approvals. A single goroutine checks every
// rollout in progress against the resource cache, so the provider loop is never blocked and many concurrent
// rollouts cost one pass over the cache per interval.
type rolloutWatcher struct {
	timeout  time.Duration
	interval time.Duration

	mu      sync.Mutex
	watches map[string]*rolloutWatch // by resource identifier
	running bool
}

type rolloutWatch struct {
	approvalID string
	target     types.RolloutTarget
}

func newRolloutWatcher() *rolloutWatcher {
	return &rolloutWatcher{
		timeout:  DefaultRolloutTimeout,
		interval: defaultRolloutInterval,
		watches:  make(map[string]*rolloutWatch),
	}
}

// SetRolloutTimeout sets how long the rollout of an approved update may take before it is reported as failed
func (p *Provider) SetRolloutTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	p.rollouts.mu.Lock()
	p.rollouts.timeout = timeout
	p.rollouts.mu.Unlock()
}

// startRollout records that the resource of an approved plan rolls out and follows the rollout
func (p *Provider) startRollout(plan *UpdatePlan) {
	resource := plan.Resource
	target := types.RolloutTarget{
		Identifier: resource.Identifier,
		Name:       resource.Name,
		Marker:     resource.GetSpecAnnotations()[types.KeelUpdateTimeAnnotation],
		State:      types.RolloutStateRolling,
		StartedAt:  time.Now(),
	}
	// what rolling the resource back needs
	for _, container := range plan.containers {
		container.PreviousDigest = plan.CurrentDigest
		container.NewDigest = plan.NewDigest
		target.Containers = append(target.Containers, container)
	}

	_, err := p.approvalManager.UpdateRollout(plan.approvalID, func(approval *types.Approval) bool {
		approval.SetRolloutTarget(target)
		return true
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"resource": resource.Identifier,
		}).Warn("provider.kubernetes: failed to record the rollout of an approved update")
		return
	}

	p.watchRollout(plan.approvalID, target)
}

// watchRollout follows the rollout of a resource until it is live, it failed, or a newer update of the resource
// took over
func (p *Provider) watchRollout(approvalID string, target types.RolloutTarget) {
	w := p.rollouts
	w.mu.Lock()
	previous := w.watches[target.Identifier]
	w.watches[target.Identifier] = &rolloutWatch{approvalID: approvalID, target: target}
	start := !w.running
	w.running = true
	w.mu.Unlock()

	if previous != nil && (previous.approvalID != approvalID || previous.target.Marker != target.Marker) {
		p.replaceRollout(previous)
	}
	if start {
		go p.runRolloutWatches()
	}
}

// runRolloutWatches checks the rollouts in progress until none is left or the provider stops
func (p *Provider) runRolloutWatches() {
	w := p.rollouts
	w.mu.Lock()
	interval := w.interval
	w.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			w.mu.Lock()
			w.running = false
			w.mu.Unlock()
			return
		case <-ticker.C:
		}

		w.mu.Lock()
		if len(w.watches) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		watches := make([]*rolloutWatch, 0, len(w.watches))
		for _, watch := range w.watches {
			watches = append(watches, watch)
		}
		timeout := w.timeout
		w.mu.Unlock()

		resources := make(map[string]*k8s.GenericResource)
		for _, resource := range p.cache.Values() {
			resources[resource.Identifier] = resource
		}

		for _, watch := range watches {
			timedOut := time.Since(watch.target.StartedAt) > timeout
			if !p.checkRollout(watch, resources[watch.target.Identifier], timedOut) {
				continue
			}
			w.mu.Lock()
			if w.watches[watch.target.Identifier] == watch {
				delete(w.watches, watch.target.Identifier)
			}
			w.mu.Unlock()
		}
	}
}

// checkRollout records the progress of a rollout and reports whether the rollout is over
func (p *Provider) checkRollout(watch *rolloutWatch, resource *k8s.GenericResource, timedOut bool) bool {
	target := watch.target
	progress := evaluateRollout(resource, target.Marker)
	if progress.Done {
		progress.Failure = ""
	} else if progress.Failure != "" || timedOut {
		progress.Failure = p.rolloutFailureReason(resource, progress.Failure)
	}

	finished := false
	_, err := p.approvalManager.UpdateRollout(watch.approvalID, func(approval *types.Approval) bool {
		current := approval.RolloutTarget(target.Identifier)
		if current == nil || current.Marker != target.Marker || current.State != types.RolloutStateRolling {
			// a newer update or a rollback of the resource took over
			finished = true
			return false
		}

		changed := current.Ready != progress.Ready || current.Desired != progress.Desired
		current.Ready = progress.Ready
		current.Desired = progress.Desired

		switch {
		case progress.Done:
			current.State = types.RolloutStateLive
		case progress.Failure != "":
			current.State = types.RolloutStateFailed
			current.Reason = progress.Failure
		default:
			return changed
		}
		current.FinishedAt = time.Now()
		finished = true
		return true
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"resource": target.Identifier,
		}).Debug("provider.kubernetes: failed to record rollout progress")
		// an approval that is gone has nothing left to report on
		return err == store.ErrRecordNotFound
	}

	return finished
}

// replaceRollout records that a newer update of the resource took over a rollout in progress
func (p *Provider) replaceRollout(previous *rolloutWatch) {
	_, err := p.approvalManager.UpdateRollout(previous.approvalID, func(approval *types.Approval) bool {
		current := approval.RolloutTarget(previous.target.Identifier)
		if current == nil || current.Marker != previous.target.Marker || current.State != types.RolloutStateRolling {
			return false
		}
		current.State = types.RolloutStateReplaced
		current.FinishedAt = time.Now()
		return true
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"resource": previous.target.Identifier,
		}).Debug("provider.kubernetes: failed to record a replaced rollout")
	}
}

// rolloutFailureReason explains a failed rollout, preferring what the pods report (ie: ImagePullBackOff,
// CrashLoopBackOff) over the reason of the workload
func (p *Provider) rolloutFailureReason(resource *k8s.GenericResource, workloadReason string) string {
	if resource == nil {
		return "resource not found"
	}
	if reason := p.podFailureReason(resource); reason != "" {
		return reason
	}
	if workloadReason != "" {
		return workloadReason
	}
	return "rollout timed out"
}

// podFailureReason returns the reason of the first container of the resource that waits for something other than
// a normal start, or that stopped without being ready. Empty when the pods can not be read.
func (p *Provider) podFailureReason(resource *k8s.GenericResource) string {
	selector, ok := resource.GetPodSelector()
	if !ok {
		return ""
	}
	pods, err := p.implementer.Pods(resource.Namespace, selector)
	if err != nil || pods == nil {
		return ""
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		statuses := append(append([]v1.ContainerStatus(nil), pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...)
		for _, status := range statuses {
			if waiting := status.State.Waiting; waiting != nil && waiting.Reason != "" && waiting.Reason != "ContainerCreating" && waiting.Reason != "PodInitializing" {
				return waiting.Reason
			}
			if terminated := status.LastTerminationState.Terminated; terminated != nil && !status.Ready && terminated.Reason != "" {
				return terminated.Reason
			}
		}
	}
	return ""
}

// rolloutProgress is the progress of a rollout as reported by the workload
type rolloutProgress struct {
	Ready   int32
	Desired int32
	Done    bool
	Failure string
}

// evaluateRollout reads the progress of a rollout from the status of the resource. The marker identifies the
// updated pod template: until the resource carries it, the update has not been observed yet.
func evaluateRollout(resource *k8s.GenericResource, marker string) rolloutProgress {
	if resource == nil || resource.GetSpecAnnotations()[types.KeelUpdateTimeAnnotation] != marker {
		return rolloutProgress{}
	}

	switch obj := resource.GetResource().(type) {
	case *apps_v1.Deployment:
		desired := replicasOrOne(obj.Spec.Replicas)
		status := obj.Status
		if status.ObservedGeneration < obj.Generation {
			return rolloutProgress{Desired: desired}
		}
		progress := rolloutProgress{
			Ready:   minInt32(status.UpdatedReplicas, status.AvailableReplicas),
			Desired: desired,
			Done: status.UpdatedReplicas == desired && status.ReadyReplicas == desired &&
				status.AvailableReplicas == desired && status.Replicas == desired,
		}
		for _, condition := range status.Conditions {
			if condition.Type == apps_v1.DeploymentProgressing && condition.Reason == "ProgressDeadlineExceeded" {
				progress.Failure = condition.Reason
			}
		}
		return progress

	case *apps_v1.StatefulSet:
		desired := replicasOrOne(obj.Spec.Replicas)
		status := obj.Status
		if status.ObservedGeneration < obj.Generation {
			return rolloutProgress{Desired: desired}
		}
		return rolloutProgress{
			Ready:   minInt32(status.UpdatedReplicas, status.ReadyReplicas),
			Desired: desired,
			Done: status.UpdatedReplicas == desired && status.ReadyReplicas == desired && status.Replicas == desired &&
				(status.UpdateRevision == "" || status.CurrentRevision == status.UpdateRevision),
		}

	case *apps_v1.DaemonSet:
		status := obj.Status
		desired := status.DesiredNumberScheduled
		if status.ObservedGeneration < obj.Generation {
			return rolloutProgress{Desired: desired}
		}
		return rolloutProgress{
			Ready:   minInt32(status.UpdatedNumberScheduled, status.NumberAvailable),
			Desired: desired,
			Done:    status.UpdatedNumberScheduled == desired && status.NumberAvailable == desired,
		}
	}

	// other resources, ie: cron jobs, have nothing to roll out
	return rolloutProgress{Done: true}
}

func replicasOrOne(replicas *int32) int32 {
	if replicas == nil {
		return 1
	}
	return *replicas
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
