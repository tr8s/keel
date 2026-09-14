package types

import (
	"testing"
)

func TestNoticeIdentifier(t *testing.T) {
	identifier := NoticeIdentifier("group/trackeid/trackeid", "sha256:62c200e9")
	if identifier != "notice/group/trackeid/trackeid:sha256:62c200e9" {
		t.Errorf("unexpected identifier %q", identifier)
	}
	if key := NoticeKey(identifier); key != "notice/group/trackeid/trackeid" {
		t.Errorf("unexpected key %q", key)
	}
}

func TestDeployNoticesCanNotBeRolledBack(t *testing.T) {
	notice := &Approval{Kind: ApprovalKindNotice, Rollout: RolloutTargets{{Identifier: "deployment/trackeid/trackeid-api", State: RolloutStateLive}}}
	if !notice.IsNotice() || notice.RollbackError() == nil {
		t.Error("expected a deploy notice to refuse rollbacks")
	}
}
