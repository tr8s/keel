package kubernetes

import (
	"strings"
	"testing"

	"github.com/keel-hq/keel/types"
)

func TestDeployNoticeEnvironmentLabel(t *testing.T) {
	f := newNoticeFixture(t, noticeDeployment("selfled", "", map[string]string{types.KeelEnvironmentAnnotation: " staging "}))

	if updated := f.push("selfled", groupDigest("a"), groupRevisionA); strings.Join(updated, ",") != "selfled" {
		t.Fatalf("expected the update to be applied, got %v", updated)
	}
	notice := f.notices()["notice/deployment/trackeid/selfled:"+groupRevisionA]
	if notice == nil || notice.Environment != "staging" {
		t.Errorf("expected the trimmed environment label on the notice, got %+v", notice)
	}
}

func TestDeployNoticeGroupEnvironmentOfFirstMember(t *testing.T) {
	f := newNoticeFixture(t,
		noticeDeployment("trackeid-api", "trackeid", nil),
		noticeDeployment("trackeid-portal", "trackeid", map[string]string{types.KeelEnvironmentAnnotation: "prod"}),
		noticeDeployment("trackeid-worker", "trackeid", map[string]string{types.KeelEnvironmentAnnotation: "staging"}),
	)

	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)
	f.push("trackeid-worker", groupDigest("c"), groupRevisionA)

	notice := f.notices()["notice/group/trackeid/trackeid:"+groupRevisionA]
	if notice == nil || len(notice.Members) != 3 || notice.Environment != "prod" {
		t.Errorf("expected the environment of the first member with one, got %+v", notice)
	}
}

func TestApprovalEnvironmentLabelIsCut(t *testing.T) {
	deployment := groupDeployment("trackeid-api", "", "1")
	deployment.Annotations[types.KeelEnvironmentAnnotation] = "  " + strings.Repeat("p", 40)
	f := newGroupFixture(t, deployment)

	if updated := f.push("trackeid-api", groupDigest("a"), groupRevisionA); len(updated) != 0 {
		t.Fatalf("expected the update to wait for approval, got %v", updated)
	}
	approval, err := f.provider.approvalManager.Get("deployment/trackeid/trackeid-api:main")
	if err != nil {
		t.Fatalf("expected an approval: %s", err)
	}
	if approval.Environment != strings.Repeat("p", types.MaxEnvironmentLength) {
		t.Errorf("expected the environment label cut to %d characters, got %q", types.MaxEnvironmentLength, approval.Environment)
	}
}

func TestGroupApprovalEnvironmentOfFirstMember(t *testing.T) {
	f := newGroupFixture(t,
		groupDeployment("trackeid-api", "trackeid", "1"),
		noticeDeployment("trackeid-portal", "trackeid", map[string]string{types.KeelMinimumApprovalsLabel: "1", types.KeelEnvironmentAnnotation: "prod"}),
		noticeDeployment("trackeid-worker", "trackeid", map[string]string{types.KeelMinimumApprovalsLabel: "1", types.KeelEnvironmentAnnotation: "staging"}),
	)

	f.push("trackeid-api", groupDigest("a"), groupRevisionA)
	f.push("trackeid-portal", groupDigest("b"), groupRevisionA)
	f.push("trackeid-worker", groupDigest("c"), groupRevisionA)

	if approval := f.onlyGroupApproval(); approval.Environment != "prod" || len(approval.Members) != 3 {
		t.Errorf("expected the environment of the first member with one, got %q with %d members", approval.Environment, len(approval.Members))
	}
}
