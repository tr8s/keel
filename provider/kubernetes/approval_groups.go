package kubernetes

import (
	"fmt"
	"strings"
	"time"

	"github.com/keel-hq/keel/types"
)

// getApprovalGroupFromMeta returns the approval group of a resource (keel.sh/approvalGroup), empty when it
// has none
func getApprovalGroupFromMeta(labels map[string]string, annotations map[string]string) string {

	searchKey := strings.ToLower(types.KeelApprovalGroupAnnotation)

	for k, v := range labels {
		if strings.ToLower(k) == searchKey {
			return strings.TrimSpace(v)
		}
	}

	for k, v := range annotations {
		if strings.ToLower(k) == searchKey {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

// isGroupApproved decides the update of a resource in an approval group. The resources of a namespace and
// group that update to the same change (the revision label of the new image, or the new tag when that is
// unknown) share one approval, so a single decision applies to all of them, including resources whose update
// arrives after the decision.
func (p *Provider) isGroupApproved(event *types.Event, plan *UpdatePlan, group string, minApprovals, deadline int) (bool, error) {
	resource := plan.Resource
	groupKey := resource.Namespace + "/" + group
	member := types.ApprovalMember{
		Identifier: resource.Identifier,
		Name:       resource.Name,
		Repository: event.Repository,
	}

	approval, joined, err := p.findGroupApproval(groupKey, member)
	if err != nil {
		return false, err
	}

	if approval == nil {
		// approval responses only resume the updates of members that asked for approval
		if event.TriggerName == types.TriggerTypeApproval.String() {
			return false, nil
		}

		req := &types.Approval{
			Provider:       types.ProviderTypeKubernetes,
			Event:          event,
			CurrentVersion: plan.CurrentVersion,
			NewVersion:     plan.NewVersion,
			CurrentDigest:  plan.CurrentDigest,
			NewDigest:      plan.NewDigest,
			VotesRequired:  minApprovals,
			Deadline:       time.Now().Add(time.Duration(deadline) * time.Hour),
			Group:          groupKey,
		}

		p.describeChange(req, plan, &event.Repository)

		change := req.NewRevision
		if change == "" {
			change = plan.NewVersion
		}
		req.Identifier = types.GroupApprovalIdentifier(resource.Namespace, group, change)
		req.Message = fmt.Sprintf("New version is available for approval group %s (%s).", groupKey, req.Delta())

		approval, err = p.approvalManager.RequestGroupApproval(req, member)
		if err != nil {
			return false, err
		}
	} else if joined.Deployed {
		// this image was already deployed with the approval
		return false, nil
	}

	if approval.Status() != types.ApprovalStatusApproved {
		return false, nil
	}

	plan.groupApproval = approval.Identifier
	plan.approvalID = approval.ID
	return true, nil
}

// findGroupApproval returns the active approval of the group that the resource already joined for the image of
// the event, along with the resource's member entry
func (p *Provider) findGroupApproval(group string, member types.ApprovalMember) (*types.Approval, *types.ApprovalMember, error) {
	approvals, err := p.approvalManager.List()
	if err != nil {
		return nil, nil, err
	}

	for _, approval := range approvals {
		// the approvals list also holds archived approvals
		if approval.Group != group || approval.Archived {
			continue
		}
		joined := approval.Member(member.Identifier)
		if joined == nil {
			continue
		}
		if joined.Repository.Digest != "" && member.Repository.Digest != "" {
			if joined.Repository.Digest == member.Repository.Digest {
				return approval, joined, nil
			}
			continue
		}
		// without digests only the tag identifies the image, so only members that were not deployed yet resume
		if joined.Repository.Tag == member.Repository.Tag && !joined.Deployed {
			return approval, joined, nil
		}
	}

	return nil, nil, nil
}
