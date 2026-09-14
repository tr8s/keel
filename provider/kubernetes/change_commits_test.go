package kubernetes

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/keel-hq/keel/types"
)

func encodeCommits(json string) string {
	return base64.StdEncoding.EncodeToString([]byte(json))
}

func TestDecodeChangeCommits(t *testing.T) {
	var seven []string
	for i := 7; i > 0; i-- {
		seven = append(seven, fmt.Sprintf(`{"sha":"%s","subject":"commit %d"}`, strings.Repeat(fmt.Sprint(i), 40), i))
	}

	tests := []struct {
		name    string
		value   string
		want    types.ChangeCommits
		wantErr bool
	}{
		{
			name:  "several commits, newest first",
			value: encodeCommits(`[{"sha":"896a024c85d9","subject":"CI retries registry calls"},{"sha":"4c1b2a3","subject":"Label images"}]`),
			want:  types.ChangeCommits{{SHA: "896a024c85d9", Subject: "CI retries registry calls"}, {SHA: "4c1b2a3", Subject: "Label images"}},
		},
		{
			name:  "at most five are kept",
			value: encodeCommits("[" + strings.Join(seven, ",") + "]"),
			want: types.ChangeCommits{
				{SHA: strings.Repeat("7", 40), Subject: "commit 7"},
				{SHA: strings.Repeat("6", 40), Subject: "commit 6"},
				{SHA: strings.Repeat("5", 40), Subject: "commit 5"},
				{SHA: strings.Repeat("4", 40), Subject: "commit 4"},
				{SHA: strings.Repeat("3", 40), Subject: "commit 3"},
			},
		},
		{
			name:  "commits without a sha are dropped",
			value: encodeCommits(`[{"subject":"no sha"},{"sha":"896a024","subject":"kept"}]`),
			want:  types.ChangeCommits{{SHA: "896a024", Subject: "kept"}},
		},
		{name: "not base64", value: "not base64!", wantErr: true},
		{name: "not JSON", value: encodeCommits(`{"sha":`), wantErr: true},
		{name: "not an array", value: encodeCommits(`{"sha":"896a024"}`), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeChangeCommits(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("decodeChangeCommits() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decodeChangeCommits() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDescribeCommits(t *testing.T) {
	approval := &types.Approval{}
	describeCommits(approval, map[string]string{
		types.KeelChangeBaseLabel:    "4c1b2a3d4e5f60718293a4b5c6d7e8f90a1b2c3d",
		types.KeelChangeCountLabel:   "3",
		types.KeelChangeCommitsLabel: encodeCommits(`[{"sha":"896a024","subject":"CI retries registry calls"}]`),
	})
	if approval.ChangeBase != "4c1b2a3d4e5f60718293a4b5c6d7e8f90a1b2c3d" || approval.ChangeCount != 3 || len(approval.ChangeCommits) != 1 {
		t.Errorf("unexpected change details: %+v", approval)
	}

	// a bad commit list and count leave the approval with the single subject
	bad := &types.Approval{}
	describeCommits(bad, map[string]string{
		types.KeelChangeCountLabel:   "several",
		types.KeelChangeCommitsLabel: "not base64!",
	})
	if bad.ChangeCount != 0 || bad.ChangeCommits != nil {
		t.Errorf("expected a bad commit list to be ignored, got %+v", bad)
	}
}

func TestGroupApprovalKeepsCommitList(t *testing.T) {
	f := newGroupFixture(t, groupDeployment("trackeid-api", "trackeid", "1"))
	f.labels.labels[groupDigest("a")] = map[string]string{
		types.OCIImageSourceLabel:    "https://github.com/tr8s/trackeid",
		types.OCIImageRevisionLabel:  groupRevisionA,
		types.KeelChangeBaseLabel:    groupRevisionB,
		types.KeelChangeCountLabel:   "3",
		types.KeelChangeCommitsLabel: encodeCommits(`[{"sha":"` + groupRevisionA + `","subject":"CI retries registry calls"},{"sha":"4c1b2a3","subject":"Label images"}]`),
	}
	f.process(types.Event{Repository: types.Repository{Name: "registry.example.com/tr8s/trackeid-api", Tag: "main", Digest: groupDigest("a")}})

	// read back from the store
	approval := f.onlyGroupApproval()
	if approval.ChangeBase != groupRevisionB || approval.ChangeCount != 3 || len(approval.ChangeCommits) != 2 || approval.ChangeCommits[0].Subject != "CI retries registry calls" {
		t.Errorf("expected the commit list to be stored on the approval, got base %q count %d commits %+v", approval.ChangeBase, approval.ChangeCount, approval.ChangeCommits)
	}
}
