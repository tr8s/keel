package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"

	manifestlist "github.com/distribution/distribution/v3/manifest/manifestlist"
	manifestv2 "github.com/distribution/distribution/v3/manifest/schema2"
	oci "github.com/opencontainers/image-spec/specs-go/v1"
)

// maxImageMetadataBytes bounds how much of a manifest or image config
// response is read.
const maxImageMetadataBytes = 4 << 20

// imageManifest holds the fields shared by image manifests and image indexes
// that are needed to find an image's labels.
type imageManifest struct {
	Manifests   []oci.Descriptor  `json:"manifests"`
	Config      oci.Descriptor    `json:"config"`
	Annotations map[string]string `json:"annotations"`
}

// ImageLabels returns the metadata an image was published with: the
// annotations of its manifest (and of its image index, for a multi-platform
// image) merged with the labels of its image config, which win on conflict.
// reference is a tag or a digest. For an image index the first manifest built
// for a real platform is inspected, since build metadata such as
// org.opencontainers.image.revision is shared by all platforms.
func (r *Registry) ImageLabels(ctx context.Context, repository, reference string) (map[string]string, error) {
	manifest, err := r.imageManifest(ctx, repository, reference)
	if err != nil {
		return nil, err
	}

	labels := map[string]string{}
	if len(manifest.Manifests) > 0 {
		maps.Copy(labels, manifest.Annotations)
		child := platformManifest(manifest.Manifests)
		if child == "" {
			return nil, fmt.Errorf("image index %s has no platform manifest", reference)
		}
		if manifest, err = r.imageManifest(ctx, repository, child); err != nil {
			return nil, err
		}
	}
	maps.Copy(labels, manifest.Annotations)

	if manifest.Config.Digest == "" {
		return labels, nil
	}

	body, err := r.get(ctx, r.url("/v2/%s/blobs/%s", repository, manifest.Config.Digest), "")
	if err != nil {
		return nil, err
	}
	var config oci.Image
	if err := json.Unmarshal(body, &config); err != nil {
		return nil, fmt.Errorf("decode image config: %w", err)
	}
	maps.Copy(labels, config.Config.Labels)

	return labels, nil
}

func (r *Registry) imageManifest(ctx context.Context, repository, reference string) (*imageManifest, error) {
	url := r.url("/v2/%s/manifests/%s", repository, reference)
	r.Logf("registry.manifest.labels url=%s repository=%s reference=%s", url, repository, reference)

	accept := strings.Join([]string{manifestlist.MediaTypeManifestList, manifestv2.MediaTypeManifest, oci.MediaTypeImageIndex, oci.MediaTypeImageManifest}, ",")
	body, err := r.get(ctx, url, accept)
	if err != nil {
		return nil, err
	}

	var manifest imageManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &manifest, nil
}

// platformManifest returns the digest of the first index entry that is an
// image for a platform, skipping attestation manifests.
func platformManifest(manifests []oci.Descriptor) string {
	for _, descriptor := range manifests {
		if descriptor.Digest == "" {
			continue
		}
		if descriptor.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
			continue
		}
		if descriptor.Platform != nil && descriptor.Platform.OS == "unknown" {
			continue
		}
		return descriptor.Digest.String()
	}
	return ""
}

// get fetches url and returns its body, failing on non-2xx responses.
func (r *Registry) get(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("request %s returned %s", url, resp.Status)
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxImageMetadataBytes))
}
