package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/opencontainers/go-digest"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestImageLabelsFromImageIndex(t *testing.T) {
	const (
		indexDigest       = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		attestationDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
		amd64Digest       = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
		configDigest      = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	)

	indexBody, _ := json.Marshal(oci.Index{
		MediaType: oci.MediaTypeImageIndex,
		Manifests: []oci.Descriptor{
			{
				Digest:      digest.Digest(attestationDigest),
				Platform:    &oci.Platform{OS: "unknown", Architecture: "unknown"},
				Annotations: map[string]string{"vnd.docker.reference.type": "attestation-manifest"},
			},
			{Digest: digest.Digest(amd64Digest), Platform: &oci.Platform{OS: "linux", Architecture: "amd64"}},
		},
		Annotations: map[string]string{"org.opencontainers.image.source": "https://github.com/tr8s/index"},
	})
	manifestBody, _ := json.Marshal(oci.Manifest{
		MediaType:   oci.MediaTypeImageManifest,
		Config:      oci.Descriptor{Digest: digest.Digest(configDigest)},
		Annotations: map[string]string{"org.opencontainers.image.created": "2026-09-14T10:00:00Z"},
	})
	configBody, _ := json.Marshal(oci.Image{
		Config: oci.ImageConfig{Labels: map[string]string{
			"org.opencontainers.image.source":   "https://github.com/tr8s/trackeid",
			"org.opencontainers.image.revision": "62c200e9",
		}},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/tr8s/trackeid/manifests/" + indexDigest:
			w.Header().Set("Content-Type", oci.MediaTypeImageIndex)
			w.Write(indexBody)
		case "/v2/tr8s/trackeid/manifests/" + amd64Digest:
			w.Header().Set("Content-Type", oci.MediaTypeImageManifest)
			w.Write(manifestBody)
		case "/v2/tr8s/trackeid/blobs/" + configDigest:
			w.Write(configBody)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := New(server.URL, "", "")
	labels, err := r.ImageLabels(context.Background(), "tr8s/trackeid", indexDigest)
	if err != nil {
		t.Fatalf("ImageLabels() error = %v", err)
	}

	want := map[string]string{
		"org.opencontainers.image.source":   "https://github.com/tr8s/trackeid",
		"org.opencontainers.image.revision": "62c200e9",
		"org.opencontainers.image.created":  "2026-09-14T10:00:00Z",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("ImageLabels() = %v, want %v", labels, want)
	}
}

func TestImageLabelsMissingManifest(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	r := New(server.URL, "", "")
	if _, err := r.ImageLabels(context.Background(), "tr8s/trackeid", "main"); err == nil {
		t.Fatal("ImageLabels() expected an error for a missing manifest")
	}
}
