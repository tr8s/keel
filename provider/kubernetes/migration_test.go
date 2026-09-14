package kubernetes

import (
	"testing"

	"github.com/keel-hq/keel/types"
)

func TestGroupApprovalIncludesMigrationOfAnyMember(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"), groupDeployment("trackeid-portal", "trackeid", "1"))
	labels := func(migrations string) map[string]string {
		return map[string]string{
			types.OCIImageSourceLabel:       "https://github.com/tr8s/trackeid",
			types.OCIImageRevisionLabel:     groupRevisionA,
			types.KeelChangeMigrationsLabel: migrations,
		}
	}
	event := func(name, digest string) types.Event {
		return types.Event{Repository: types.Repository{Name: "registry.example.com/tr8s/" + name, Tag: "main", Digest: digest}}
	}

	f.labels.labels[groupDigest("a")] = labels("false")
	f.labels.labels[groupDigest("b")] = labels("true")

	f.process(event("trackeid-api", groupDigest("a")))
	if f.onlyGroupApproval().IncludesMigration {
		t.Fatal("did not expect a migration for the api image")
	}

	f.process(event("trackeid-portal", groupDigest("b")))
	if approval := f.onlyGroupApproval(); !approval.IncludesMigration || len(approval.Members) != 2 {
		t.Errorf("expected the migration of the portal image to mark the group approval, got %+v", approval)
	}
}

func TestApprovalWithoutMigrationLabel(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"))
	f.push("trackeid-api", groupDigest("a"), groupRevisionA)

	if f.onlyGroupApproval().IncludesMigration {
		t.Error("did not expect a migration without the label")
	}
}
