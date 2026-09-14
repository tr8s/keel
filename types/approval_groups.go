package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// GroupApprovalIdentifier - identifier of the approval shared by the workloads of an approval group that
// update to the same change, ie: group/trackeid/trackeid:8f714c1a2b3c...
func GroupApprovalIdentifier(namespace, group, change string) string {
	return "group/" + namespace + "/" + group + ":" + change
}

// ApprovalMember - a workload decided by a group approval
type ApprovalMember struct {
	// Identifier of the resource, ie: deployment/trackeid/trackeid-api
	Identifier string `json:"identifier"`
	// Name of the resource, ie: trackeid-api
	Name string `json:"name"`
	// Repository of the update the member waits for, submitted again once the approval is approved
	Repository Repository `json:"repository"`
	// Deployed is set once the member was updated with the approval
	Deployed bool `json:"deployed,omitempty"`
}

// ApprovalMembers is stored as a JSON blob
type ApprovalMembers []ApprovalMember

func (m ApprovalMembers) Value() (driver.Value, error) {
	return json.Marshal(m)
}

func (m *ApprovalMembers) Scan(src interface{}) error {
	var source []byte
	switch value := src.(type) {
	case []byte:
		source = value
	case string:
		source = []byte(value)
	default:
		return errors.New("type assertion .([]byte) failed.")
	}
	return json.Unmarshal(source, m)
}

// Member - the member entry of the workload, nil when the workload is not a member
func (a *Approval) Member(identifier string) *ApprovalMember {
	for i := range a.Members {
		if a.Members[i].Identifier == identifier {
			return &a.Members[i]
		}
	}
	return nil
}

// SetMember - add the member, or replace the entry of the same workload when it waits for another image.
// Reports whether the members changed.
func (a *Approval) SetMember(member ApprovalMember) bool {
	if existing := a.Member(member.Identifier); existing != nil {
		if existing.Repository.Digest == member.Repository.Digest && existing.Repository.Tag == member.Repository.Tag {
			return false
		}
		*existing = member
		return true
	}
	a.Members = append(a.Members, member)
	return true
}
