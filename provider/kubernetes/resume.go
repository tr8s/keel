package kubernetes

import (
	"time"

	"github.com/keel-hq/keel/types"

	log "github.com/sirupsen/logrus"
)

// DefaultResumeDelay - how long a started provider lets its resources load before it resumes rollouts
const DefaultResumeDelay = 15 * time.Second

// ResumeRollouts makes the provider resume, once started and after the delay that lets its resources load, the
// rollouts that were in progress and the rollbacks that were requested but not applied when Keel stopped
func (p *Provider) ResumeRollouts(delay time.Duration) {
	p.resumeEnabled = true
	p.resumeDelay = delay
}

// resumeRollouts follows the rollouts that were still in progress and applies the rollbacks that were requested
// but not applied before a restart. A rollout that finished or timed out meanwhile is closed on its first check,
// so no approval message keeps showing a rollout in progress.
func (p *Provider) resumeRollouts() {
	approvals, err := p.approvalManager.ListRollouts()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Error("provider.kubernetes: failed to list the rollouts to resume")
		return
	}

	for _, approval := range approvals {
		if approval.Provider != types.ProviderTypeKubernetes {
			continue
		}

		if rollbackPending(approval) {
			log.WithFields(log.Fields{
				"approval":    approval.Identifier,
				"rolled_back": approval.RolledBackBy,
			}).Info("provider.kubernetes: applying a rollback requested before the restart")
			p.rollback(approval)
			continue
		}

		for _, target := range approval.Rollout {
			if target.State == types.RolloutStateRolling {
				p.watchRollout(approval.ID, target)
			}
		}
	}
}

// rollbackPending reports whether a rollback of the approval was requested but not applied: none of its rollouts
// started after the request
func rollbackPending(approval *types.Approval) bool {
	if approval.RolledBackBy == "" || approval.RolledBackAt == nil {
		return false
	}
	for _, target := range approval.Rollout {
		if !target.StartedAt.Before(*approval.RolledBackAt) {
			return false
		}
	}
	return true
}
