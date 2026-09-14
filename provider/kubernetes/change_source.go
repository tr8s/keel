package kubernetes

import (
	"context"
	"time"

	"github.com/keel-hq/keel/extension/credentialshelper"
	"github.com/keel-hq/keel/internal/k8s"
	"github.com/keel-hq/keel/registry"
	"github.com/keel-hq/keel/types"
	"github.com/keel-hq/keel/util/image"
	"github.com/keel-hq/keel/util/scm"

	log "github.com/sirupsen/logrus"
)

// imageLabelsTimeout bounds the registry lookups made to describe the code
// change behind an approval, so they never hold up an update for long.
const imageLabelsTimeout = 10 * time.Second

// ImageLabelsGetter reads the labels and annotations of an image from its
// registry. registry.DefaultClient implements it.
type ImageLabelsGetter interface {
	ImageLabels(ctx context.Context, opts registry.Opts) (map[string]string, error)
}

// SetImageLabelsGetter enables describing the code change behind approval
// requests, based on the org.opencontainers.image.source and
// org.opencontainers.image.revision labels of the new and the running image.
func (p *Provider) SetImageLabelsGetter(getter ImageLabelsGetter) {
	p.imageLabels = getter
}

// describeChange fills the source repository and revisions of an approval
// from the image labels. It is best effort: when the labels are missing or the
// registry cannot be reached the approval is left as it is.
func (p *Provider) describeChange(approval *types.Approval, plan *UpdatePlan, repo *types.Repository) {
	if p.imageLabels == nil || plan.Resource == nil {
		return
	}

	ref, err := image.Parse(repo.String())
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), imageLabelsTimeout)
	defer cancel()

	// the digest identifies the image the event was about, the tag may have
	// moved on since
	newReference := plan.NewDigest
	if newReference == "" {
		newReference = plan.NewVersion
	}
	newLabels, err := p.getImageLabels(ctx, ref, plan.Resource, newReference)
	if err != nil {
		log.WithFields(log.Fields{
			"error":     err,
			"image":     ref.Repository(),
			"reference": newReference,
		}).Debug("provider.kubernetes: failed to read new image labels, approval will not describe the code change")
		return
	}

	approval.CommitSubject = newLabels[types.KeelCommitSubjectLabel]
	approval.CommitAuthor = newLabels[types.KeelCommitAuthorLabel]
	approval.IncludesMigration = newLabels[types.KeelChangeMigrationsLabel] == "true"
	describeCommits(approval, newLabels)

	approval.NewRevision = newLabels[types.OCIImageRevisionLabel]
	if approval.NewRevision == "" {
		log.WithFields(log.Fields{
			"image":     ref.Repository(),
			"reference": newReference,
		}).Debug("provider.kubernetes: new image has no revision label, approval will not describe the code change")
		return
	}
	approval.SourceURL = newLabels[types.OCIImageSourceLabel]

	// a moving tag already points at the new image, so the running image can
	// only be looked up by digest
	currentReference := plan.CurrentDigest
	if currentReference == "" && plan.CurrentVersion != plan.NewVersion {
		currentReference = plan.CurrentVersion
	}
	if approval.SourceURL == "" || currentReference == "" || currentReference == newReference {
		return
	}

	currentLabels, err := p.getImageLabels(ctx, ref, plan.Resource, currentReference)
	if err != nil {
		log.WithFields(log.Fields{
			"error":     err,
			"image":     ref.Repository(),
			"reference": currentReference,
		}).Debug("provider.kubernetes: failed to read current image labels, approval will not link to the changes")
		return
	}
	if scm.NormalizeSource(currentLabels[types.OCIImageSourceLabel]) != scm.NormalizeSource(approval.SourceURL) {
		return
	}
	approval.CurrentRevision = currentLabels[types.OCIImageRevisionLabel]
}

// getImageLabels reads the labels of an image in the repository of ref, using
// the same registry credentials as polling that image.
func (p *Provider) getImageLabels(ctx context.Context, ref *image.Reference, resource *k8s.GenericResource, reference string) (map[string]string, error) {
	opts := registry.Opts{
		Registry: ref.Scheme() + "://" + ref.Registry(),
		Name:     ref.ShortName(),
		Tag:      reference,
	}

	var secrets []string
	if specifiedSecret := getImagePullSecretFromMeta(resource.GetLabels(), resource.GetAnnotations()); specifiedSecret != "" {
		secrets = append(secrets, specifiedSecret)
	}
	secrets = append(secrets, resource.GetImagePullSecrets()...)

	creds, err := credentialshelper.GetCredentials(&types.TrackedImage{
		Image:     ref,
		Provider:  ProviderName,
		Namespace: resource.Namespace,
		Secrets:   secrets,
	})
	if err == nil {
		opts.Username = creds.Username
		opts.Password = creds.Password
	}

	return p.imageLabels.ImageLabels(ctx, opts)
}
