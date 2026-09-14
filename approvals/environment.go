package approvals

import (
	"github.com/keel-hq/keel/types"

	log "github.com/sirupsen/logrus"
)

// mergeEnvironment - take the environment label of a member when the approval or notice has none yet. The first
// member with a label decides; a different label of a later member is logged and not shown. Reports whether the
// environment changed.
func mergeEnvironment(approval *types.Approval, environment, member string) bool {
	switch {
	case environment == "" || environment == approval.Environment:
		return false
	case approval.Environment == "":
		approval.Environment = environment
		return true
	}
	log.WithFields(log.Fields{
		"identifier":  approval.Identifier,
		"member":      member,
		"environment": environment,
		"shown":       approval.Environment,
	}).Debug("approvals.manager: members disagree on their environment, showing the first")
	return false
}
