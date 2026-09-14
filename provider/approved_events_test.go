package provider

import (
	"testing"

	"github.com/keel-hq/keel/types"
)

func TestApprovedEvents(t *testing.T) {
	single := &types.Approval{Event: &types.Event{Repository: types.Repository{Name: "app", Tag: "1.2.3"}}}
	events := ApprovedEvents(single)
	if len(events) != 1 || events[0].Repository.Name != "app" || events[0].TriggerName != types.TriggerTypeApproval.String() {
		t.Errorf("expected the approval event, got %+v", events)
	}

	group := &types.Approval{Members: types.ApprovalMembers{
		{Identifier: "deployment/ns/api", Repository: types.Repository{Name: "api", Tag: "main", Digest: "sha256:a"}},
		{Identifier: "deployment/ns/worker", Repository: types.Repository{Name: "api", Tag: "main", Digest: "sha256:a"}},
		{Identifier: "deployment/ns/portal", Repository: types.Repository{Name: "portal", Tag: "main", Digest: "sha256:b"}, Deployed: true},
	}}
	events = ApprovedEvents(group)
	if len(events) != 1 || events[0].Repository.Name != "api" || events[0].Repository.Digest != "sha256:a" || events[0].TriggerName != types.TriggerTypeApproval.String() {
		t.Errorf("expected one event for the members not deployed yet, got %+v", events)
	}
}
