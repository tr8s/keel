package types

import (
	"errors"
	"fmt"
)

// KeelTrackedImagesAnnotation - images Keel keeps tracking for the containers that a rollback pinned by digest,
// as a JSON object of container name to image reference, ie: {"api":"registry.example.com/app:main"}
const KeelTrackedImagesAnnotation = "keel.sh/trackedImages"

// KeelRollbackHoldAnnotation - images that were rolled back on a resource and are not offered again, as a JSON
// object of image repository to the rolled back digest and revision
const KeelRollbackHoldAnnotation = "keel.sh/rollbackHold"

// RolloutContainer - a container changed by an approved update and what rolling it back needs
type RolloutContainer struct {
	// Name of the container
	Name string `json:"name"`
	// Image is the reference the update set, ie: registry.example.com/app:main. It stays tracked while the
	// container is rolled back.
	Image string `json:"image"`
	// PreviousDigest is the digest the container ran before the update, a rollback pins it
	PreviousDigest string `json:"previousDigest,omitempty"`
	// NewDigest is the digest of the update, held once the update is rolled back
	NewDigest string `json:"newDigest,omitempty"`
}

// ErrAlreadyRolledBack - the approved updates were already rolled back
var ErrAlreadyRolledBack = errors.New("the approved update was already rolled back")

// RollbackError - why the updates of the approval can not be rolled back, nil when they can
func (a *Approval) RollbackError() error {
	switch {
	case a.IsNotice():
		return errors.New("updates without approval can not be rolled back")
	case a.RolledBackBy != "":
		return ErrAlreadyRolledBack
	case a.RollbackFailure != "":
		return errors.New(a.RollbackFailure)
	case len(a.Rollout) == 0:
		return errors.New("nothing was deployed with this approval")
	}

	for _, target := range a.Rollout {
		if target.State == RolloutStateReplaced {
			return fmt.Errorf("%s was updated again since", target.Name)
		}
		if len(target.Containers) == 0 {
			return fmt.Errorf("the previous image of %s is unknown", target.Name)
		}
		for _, container := range target.Containers {
			if container.PreviousDigest == "" {
				return fmt.Errorf("the previous image of %s is unknown", target.Name)
			}
		}
	}
	return nil
}
