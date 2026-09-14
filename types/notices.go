package types

import (
	"strings"
)

// ApprovalKindNotice - the kind of the records that follow the deploy notice of an update that needed no approval.
// Deploy notices share the approvals table so that rollouts, message locations and restarts work the same way, but
// they are archived from the start and never listed or decided as approvals.
const ApprovalKindNotice = "notice"

// DeployNoticeMetadataKey - notification metadata set on the success notification of an update that a deploy notice
// reports, so that the Slack notification sender does not report it twice
const DeployNoticeMetadataKey = "deployNotice"

// IsNotice - whether the record follows a deploy notice rather than an approval
func (a *Approval) IsNotice() bool {
	return a.Kind == ApprovalKindNotice
}

// NoticeIdentifier - identifier of the deploy notice of a change of a resource or of an approval group,
// ie: notice/deployment/selfled/selfled-api:4c80a47... or notice/group/trackeid/trackeid:8b4d196...
func NoticeIdentifier(key, change string) string {
	return "notice/" + key + ":" + change
}

// NoticeKey - the resource or group part of a deploy notice identifier, ie: notice/group/trackeid/trackeid
func NoticeKey(identifier string) string {
	key, _, _ := strings.Cut(identifier, ":")
	return key
}
