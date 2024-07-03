// Copyright (c) 2021-2023 SUSE LLC

package imagescan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/reugn/go-quartz/quartz"
	"golang.org/x/sync/semaphore"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/kstatus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// AlphabeticalOrderDesc descending order
	AlphabeticalOrderDesc = "DESC"
)

var _ quartz.Job = &TagScanJob{}

type TagScanJob struct {
	sem    *semaphore.Weighted
	client client.Client

	namespace string
	name      string
}

func tagScanDescription(namespace string, name string) string {
	return fmt.Sprintf("image-tag-scan-%s-%s", namespace, name)
}

func TagScanKey(namespace string, name string) *quartz.JobKey {
	return quartz.NewJobKey(tagScanDescription(namespace, name))
}

func NewTagScanJob(c client.Client, namespace string, name string) *TagScanJob {
	return &TagScanJob{
		sem:    semaphore.NewWeighted(1),
		client: c,

		namespace: namespace,
		name:      name,
	}
}

func (j *TagScanJob) Execute(ctx context.Context) error {
	if !j.sem.TryAcquire(1) {
		// already running
		return nil
	}
	defer j.sem.Release(1)

	j.updateImageTags(ctx)

	return nil
}

func (j *TagScanJob) Description() string {
	return tagScanDescription(j.namespace, j.name)
}

func (j *TagScanJob) updateImageTags(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("imagescan-tag-scanner")
	nsn := types.NamespacedName{Namespace: j.namespace, Name: j.name}

	image := &fleet.ImageScan{}
	err := j.client.Get(ctx, nsn, image)
	if err != nil {
		return
	}

	if image.Spec.Suspend {
		return
	}

	logger = logger.WithValues("name", image.Name, "namespace", image.Namespace, "gitrepo", image.Spec.GitRepoName)

	ref, err := name.ParseReference(image.Spec.Image)
	if err != nil {
		err = j.updateErrorStatus(ctx, image, err)
		logger.V(1).Error(err, "Failed to parse image name", "image", image.Spec.Image)
		return
	}

	if !shouldScan(image) {
		return
	}

	canonical := ref.Context().String()
	if canonical != image.Status.CanonicalImageName {
		image.Status.CanonicalImageName = canonical
	}

	var options []remote.Option
	if image.Spec.SecretRef != nil {
		secret := &corev1.Secret{}
		err := j.client.Get(ctx, types.NamespacedName{Namespace: image.Namespace, Name: image.Spec.SecretRef.Name}, secret)
		if err != nil {
			err = j.updateErrorStatus(ctx, image, err)
			logger.Error(err, "Failed to get image secret")
			return
		}

		auth, err := authFromSecret(secret, ref.Context().RegistryStr())
		if err != nil {
			err = j.updateErrorStatus(ctx, image, err)
			logger.Error(err, "Failed to build auth info from secret")
			return
		}
		options = append(options, remote.WithAuth(auth))
	}

	tags, err := remote.List(ref.Context(), append(options, remote.WithContext(ctx))...)
	if err != nil {
		err = j.updateErrorStatus(ctx, image, err)
		logger.Error(err, "Failed to list remote tags")
		return
	}

	image.Status.LastScanTime = metav1.NewTime(time.Now())

	latestTag, err := latestTag(image.Spec.Policy, tags)
	if err != nil {
		err = j.updateErrorStatus(ctx, image, err)
		logger.Error(err, "Failed get the digest", "latestImage", image.Status.LatestImage)
		return
	}

	image.Status.LatestTag = latestTag
	image.Status.LatestImage = image.Status.CanonicalImageName + ":" + latestTag
	digest, err := getDigest(image.Status.LatestImage, options...)
	if err != nil {
		err = j.updateErrorStatus(ctx, image, err)
		logger.Error(err, "Failed get the digest", "latestImage", image.Status.LatestImage)
		return
	}
	image.Status.LatestDigest = digest

	condition.Cond(fleet.ImageScanScanCondition).SetError(&image.Status, "", nil)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.ImageScan{}
		err := j.client.Get(ctx, nsn, t)
		if err != nil {
			return err
		}
		t.Status = image.Status
		return j.client.Status().Update(ctx, t)
	})
	if err != nil {
		logger.Error(err, "Failed to update image scan status", "status", image.Status)
	}
}

func (j *TagScanJob) updateErrorStatus(ctx context.Context, image *fleet.ImageScan, orgErr error) error {
	nsn := types.NamespacedName{Name: image.Name, Namespace: image.Namespace}

	condition.Cond(fleet.ImageScanScanCondition).SetError(&image.Status, "", orgErr)
	kstatus.SetError(image, orgErr.Error())
	merr := []error{orgErr}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.ImageScan{}
		err := j.client.Get(ctx, nsn, t)
		if err != nil {
			return err
		}
		t.Status = image.Status
		err = j.client.Status().Update(ctx, t)
		return err
	})
	if err != nil {
		merr = append(merr, err)
	}
	return errutil.NewAggregate(merr)
}

func shouldScan(image *fleet.ImageScan) bool {
	if image.Status.LatestTag == "" {
		return true
	}

	interval := image.Spec.Interval
	if interval.Seconds() == 0.0 {
		interval = metav1.Duration{
			Duration: durations.DefaultImageInterval,
		}
	}
	if time.Since(image.Status.LastScanTime.Time) < interval.Duration {
		return false
	}
	return true
}

func getDigest(image string, options ...remote.Option) (string, error) {
	nameRef, err := name.ParseReference(image)
	if err != nil {
		return "", err
	}

	im, err := remote.Image(nameRef, options...)
	if err != nil {
		return "", err
	}
	digest, err := im.Digest()
	if err != nil {
		return "", err
	}
	return digest.String(), nil
}

// authFromSecret creates an Authenticator that can be given to the
// `remote` funcs, from a Kubernetes secret. If the secret doesn't
// have the right format or data, it returns an error.
func authFromSecret(secret *corev1.Secret, registry string) (authn.Authenticator, error) {
	switch secret.Type {
	case "kubernetes.io/dockerconfigjson":
		var dockerconfig struct {
			Auths map[string]authn.AuthConfig
		}
		configData := secret.Data[".dockerconfigjson"]
		if err := json.NewDecoder(bytes.NewBuffer(configData)).Decode(&dockerconfig); err != nil {
			return nil, err
		}
		auth, ok := dockerconfig.Auths[registry]
		if !ok {
			return nil, fmt.Errorf("auth for %q not found in secret %v", registry, types.NamespacedName{Name: secret.GetName(), Namespace: secret.GetNamespace()})
		}
		return authn.FromConfig(auth), nil
	default:
		return nil, fmt.Errorf("unknown secret type %q", secret.Type)
	}
}

func latestTag(policy fleet.ImagePolicyChoice, versions []string) (string, error) {
	if len(versions) == 0 {
		return "", errors.New("no tag found")
	}
	switch {
	case policy.SemVer != nil:
		return semverLatest(policy.SemVer.Range, versions)
	case policy.Alphabetical != nil:
		var des bool
		if policy.Alphabetical.Order == "" {
			des = true
		} else {
			des = strings.ToUpper(policy.Alphabetical.Order) == AlphabeticalOrderDesc
		}
		var latest string
		for _, version := range versions {
			if latest == "" {
				latest = version
				continue
			}

			if version >= latest && des {
				latest = version
			}

			if version <= latest && !des {
				latest = version
			}
		}
		return latest, nil
	default:
		return semverLatest("*", versions)
	}
}

func semverLatest(r string, versions []string) (string, error) {
	constraints, err := semver.NewConstraint(r)
	if err != nil {
		return "", err
	}
	var latestVersion *semver.Version
	for _, version := range versions {
		if ver, err := semver.NewVersion(version); err == nil {
			if latestVersion == nil || ver.GreaterThan(latestVersion) {
				if constraints.Check(ver) {
					latestVersion = ver
				}
			}
		}
	}
	if latestVersion == nil {
		return "", fmt.Errorf("no available version matching %s", r)
	}
	return latestVersion.Original(), nil
}
