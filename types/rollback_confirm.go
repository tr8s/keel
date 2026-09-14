package types

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrRollbackChanged - the approval changed since the rollback was confirmed, ie: it was rolled back or rolled out
// again
var ErrRollbackChanged = errors.New("the rollout changed since the rollback was confirmed")

// RollbackFingerprint - identifies what a rollback of the approval would set back: the new revision and every
// rolled out resource with the update it received and the images it ran before. It changes when the approval is
// rolled back or rolled out again, so a confirmation shown before can be told apart.
func (a *Approval) RollbackFingerprint() string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%s\n", a.NewRevision)
	for _, target := range a.Rollout {
		fmt.Fprintf(hash, "%s %s\n", target.Identifier, target.Marker)
		for _, container := range target.Containers {
			fmt.Fprintf(hash, "%s %s %s\n", container.Name, container.Image, container.PreviousDigest)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}
