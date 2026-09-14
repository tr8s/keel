package kubernetes

import (
	"fmt"
	"strings"

	"github.com/keel-hq/keel/types"

	log "github.com/sirupsen/logrus"
)

// EnableDeployNotices makes the provider record a deploy notice for every update that needs no approval and follow
// its rollout on the notice, so that the Slack bot reports it
func (p *Provider) EnableDeployNotices() {
	p.deployNotices = true
}

// prepareDeployNotice describes the deploy notice of a plan that needs no approval: the change, the environment
// label, the channel (the first keel.sh/notify channel, the default channel when there is none) and, for a resource
// of an approval group, the group. The notice is recorded once the update is applied.
func (p *Provider) prepareDeployNotice(event *types.Event, plan *UpdatePlan) {
	if !p.deployNotices || p.approvalManager == nil || plan.Resource == nil {
		return
	}
	resource := plan.Resource
	labels, annotations := resource.GetLabels(), resource.GetAnnotations()

	notice := &types.Approval{
		Provider:       types.ProviderTypeKubernetes,
		Event:          event,
		CurrentVersion: plan.CurrentVersion,
		NewVersion:     plan.NewVersion,
		CurrentDigest:  plan.CurrentDigest,
		NewDigest:      plan.NewDigest,
		Environment:    types.ParseEnvironment(labels, annotations),
	}
	p.describeChange(notice, plan, &event.Repository)

	// the members of a group share the notice of a revision or, without one, of a tag, like group approvals do
	key, change := resource.Identifier, notice.NewRevision
	if group := getApprovalGroupFromMeta(labels, annotations); group != "" {
		notice.Group = resource.Namespace + "/" + group
		key = "group/" + notice.Group
	} else if change == "" {
		// the digest tells the images of a moving tag apart
		change = plan.NewDigest
	}
	if change == "" {
		change = plan.NewVersion
	}
	notice.Identifier = types.NoticeIdentifier(key, change)

	if channels := types.ParseEventNotificationChannels(annotations); len(channels) > 0 {
		notice.MessageChannel = strings.TrimPrefix(channels[0], "#")
	}
	notice.Message = fmt.Sprintf("Deploying %s/%s (%s).", resource.Namespace, resource.Name, notice.Delta())

	plan.notice = notice
}

// recordDeployNotice records the deploy notice of an applied update, joining the notice of the same change of its
// resource or group, so that the rollout of the update is followed on the notice. It reports whether the notice was
// recorded.
func (p *Provider) recordDeployNotice(plan *UpdatePlan) bool {
	resource := plan.Resource
	member := types.ApprovalMember{Identifier: resource.Identifier, Name: resource.Name}
	if plan.notice.Event != nil {
		member.Repository = plan.notice.Event.Repository
	}

	notice, err := p.approvalManager.RecordDeployNotice(plan.notice, member)
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err,
			"resource": resource.Identifier,
		}).Warn("provider.kubernetes: failed to record the deploy notice of an update")
		return false
	}

	plan.approvalID = notice.ID
	return true
}
