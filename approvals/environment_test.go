package approvals

import (
	"testing"
	"time"

	"github.com/keel-hq/keel/types"
)

func TestEnvironmentOfTheFirstMemberWithOne(t *testing.T) {
	steps := []struct {
		member      string
		environment string
	}{
		{member: "trackeid-api"},
		{member: "trackeid-portal", environment: "prod"},
		{member: "trackeid-worker", environment: "staging"},
	}

	t.Run("group approval", func(t *testing.T) {
		am := newNoticeManager(t)
		identifier := types.GroupApprovalIdentifier("trackeid", "trackeid", noticeRevisionA)
		for _, step := range steps {
			req := &types.Approval{
				Provider:      types.ProviderTypeKubernetes,
				Identifier:    identifier,
				Group:         "trackeid/trackeid",
				NewVersion:    "main",
				NewRevision:   noticeRevisionA,
				VotesRequired: 1,
				Deadline:      time.Now().Add(time.Hour),
				Environment:   step.environment,
			}
			if _, err := am.RequestGroupApproval(req, noticeMember(step.member)); err != nil {
				t.Fatalf("failed to request group approval: %s", err)
			}
		}
		approval, err := am.Get(identifier)
		if err != nil {
			t.Fatalf("expected the group approval: %s", err)
		}
		if approval.Environment != "prod" || len(approval.Members) != 3 {
			t.Errorf("expected the environment of the first member with one, got %q with %d members", approval.Environment, len(approval.Members))
		}
	})

	t.Run("deploy notice", func(t *testing.T) {
		am := newNoticeManager(t)
		var id string
		for _, step := range steps {
			req := noticeRequest(noticeRevisionA)
			req.Environment = step.environment
			id = recordNotice(t, am, req, step.member).ID
		}
		notice := noticeByID(t, am, id)
		if notice.Environment != "prod" || len(notice.Members) != 3 {
			t.Errorf("expected the environment of the first member with one, got %q with %d members", notice.Environment, len(notice.Members))
		}
	})
}
