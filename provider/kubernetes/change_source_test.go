package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/keel-hq/keel/internal/k8s"
	"github.com/keel-hq/keel/registry"
	"github.com/keel-hq/keel/types"

	apps_v1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeImageLabels struct {
	labels    map[string]map[string]string // by image reference
	err       error
	requested []registry.Opts
}

func (f *fakeImageLabels) ImageLabels(ctx context.Context, opts registry.Opts) (map[string]string, error) {
	f.requested = append(f.requested, opts)
	if f.err != nil {
		return nil, f.err
	}
	labels, ok := f.labels[opts.Tag]
	if !ok {
		return nil, errors.New("manifest unknown")
	}
	return labels, nil
}

func TestApprovalDescribesCodeChange(t *testing.T) {
	const (
		currentDigest   = "sha256:5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
		newDigest       = "sha256:62c200e9a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5"
		currentRevision = "5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b"
		newRevision     = "62c200e9a1b2c3d4e5f60718293a4b5c6d7e8f90"
		source          = "https://github.com/tr8s/trackeid"
	)

	tests := []struct {
		name            string
		getter          *fakeImageLabels
		wantSource      string
		wantCurrentRev  string
		wantNewRevision string
		wantSubject     string
		wantAuthor      string
	}{
		{
			name: "both revisions known",
			getter: &fakeImageLabels{labels: map[string]map[string]string{
				newDigest: {
					types.OCIImageSourceLabel:    source,
					types.OCIImageRevisionLabel:  newRevision,
					types.KeelCommitSubjectLabel: "Label images with their commit and source",
					types.KeelCommitAuthorLabel:  "Tim Brandin",
				},
				currentDigest: {types.OCIImageSourceLabel: source + ".git", types.OCIImageRevisionLabel: currentRevision},
			}},
			wantSource:      source,
			wantCurrentRev:  currentRevision,
			wantNewRevision: newRevision,
			wantSubject:     "Label images with their commit and source",
			wantAuthor:      "Tim Brandin",
		},
		{
			name: "running image not found",
			getter: &fakeImageLabels{labels: map[string]map[string]string{
				newDigest: {types.OCIImageSourceLabel: source, types.OCIImageRevisionLabel: newRevision},
			}},
			wantSource:      source,
			wantNewRevision: newRevision,
		},
		{
			name:   "registry unavailable",
			getter: &fakeImageLabels{err: errors.New("connection refused")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := &fakeImplementer{}
			fp.namespaces = &v1.NamespaceList{Items: []v1.Namespace{{ObjectMeta: meta_v1.ObjectMeta{Name: "trackeid"}}}}
			deployments := []*apps_v1.Deployment{
				{
					ObjectMeta: meta_v1.ObjectMeta{
						Name:      "trackeid-portal",
						Namespace: "trackeid",
						Labels: map[string]string{
							types.KeelPolicyLabel:           "force",
							types.KeelForceTagMatchLabel:    "true",
							types.KeelMinimumApprovalsLabel: "1",
						},
						Annotations: map[string]string{types.KeelDigestAnnotation: currentDigest},
					},
					Spec: apps_v1.DeploymentSpec{
						Template: v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{{Image: "registry.example.com/tr8s/trackeid-portal:main"}},
							},
						},
					},
				},
			}

			grc := &k8s.GenericResourceCache{}
			grc.Add(MustParseGRS(deployments)...)

			approver, teardown := approver()
			defer teardown()
			provider, err := NewProvider(fp, &fakeSender{}, approver, grc)
			if err != nil {
				t.Fatalf("failed to get provider: %s", err)
			}
			provider.SetImageLabelsGetter(tt.getter)

			repo := types.Repository{Name: "registry.example.com/tr8s/trackeid-portal", Tag: "main", Digest: newDigest}
			if _, err := provider.processEvent(&types.Event{Repository: repo}); err != nil {
				t.Fatalf("failed to process event: %s", err)
			}

			approval, err := provider.approvalManager.Get("deployment/trackeid/trackeid-portal:main")
			if err != nil {
				t.Fatalf("failed to find approval, err: %s", err)
			}

			if approval.SourceURL != tt.wantSource || approval.CurrentRevision != tt.wantCurrentRev || approval.NewRevision != tt.wantNewRevision {
				t.Errorf("got source %q, revisions %q -> %q, want %q, %q -> %q",
					approval.SourceURL, approval.CurrentRevision, approval.NewRevision,
					tt.wantSource, tt.wantCurrentRev, tt.wantNewRevision)
			}
			if approval.CommitSubject != tt.wantSubject || approval.CommitAuthor != tt.wantAuthor {
				t.Errorf("got commit %q by %q, want %q by %q", approval.CommitSubject, approval.CommitAuthor, tt.wantSubject, tt.wantAuthor)
			}
			if !strings.Contains(approval.Message, "(main@sha256:5f55a51b -> main@sha256:62c200e9)") {
				t.Errorf("unexpected approval message: %s", approval.Message)
			}
			if len(tt.getter.requested) == 0 || tt.getter.requested[0].Registry != "https://registry.example.com" || tt.getter.requested[0].Name != "tr8s/trackeid-portal" {
				t.Errorf("unexpected registry lookups: %+v", tt.getter.requested)
			}
		})
	}
}
