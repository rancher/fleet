// Package image registers a controller for image scans. (fleetcontroller)
package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/Masterminds/semver/v3"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/update"

	"github.com/rancher/wrangler/pkg/condition"
	corev1controler "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kstatus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

var (
	lock sync.Mutex

	defaultInterval = durations.DefaultImageInterval
)

const (
	// AlphabeticalOrderAsc ascending order
	AlphabeticalOrderAsc = "ASC"
	// AlphabeticalOrderDesc descending order
	AlphabeticalOrderDesc = "DESC"

	defaultMessageTemplate = `Update from image update automation`

	imageScanCond = "ImageScanned"

	imageSyncCond = "ImageSynced"
)

func Register(ctx context.Context, core corev1controler.Interface, gitRepos fleetcontrollers.GitRepoController, images fleetcontrollers.ImageScanController) {
	h := handler{
		ctx:         ctx,
		secretCache: core.Secret().Cache(),
		gitrepos:    gitRepos,
		imagescans:  images,
	}

	fleetcontrollers.RegisterImageScanStatusHandler(ctx, images, imageScanCond, "image-scan", h.onChange)

	fleetcontrollers.RegisterGitRepoStatusHandler(ctx, gitRepos, imageSyncCond, "image-sync", h.onChangeGitRepo)
}

type handler struct {
	ctx         context.Context
	secretCache corev1controler.SecretCache
	gitrepos    fleetcontrollers.GitRepoController
	imagescans  fleetcontrollers.ImageScanController
}

func (h handler) onChange(image *v1alpha1.ImageScan, status v1alpha1.ImageScanStatus) (v1alpha1.ImageScanStatus, error) {
	if image == nil || image.DeletionTimestamp != nil {
		return status, nil
	}

	if image.Spec.Suspend {
		return status, nil
	}

	ref, err := name.ParseReference(image.Spec.Image)
	if err != nil {
		kstatus.SetError(image, err.Error())
		return status, err
	}

	canonical := ref.Context().String()
	if canonical != status.CanonicalImageName {
		status.CanonicalImageName = canonical
	}

	if !shouldScan(image) {
		return status, nil
	}

	var options []remote.Option
	if image.Spec.SecretRef != nil {
		secret, err := h.secretCache.Get(image.Namespace, image.Spec.SecretRef.Name)
		if err != nil {
			kstatus.SetError(image, err.Error())
			return status, err
		}
		auth, err := authFromSecret(secret, ref.Context().RegistryStr())
		if err != nil {
			kstatus.SetError(image, err.Error())
			return status, err
		}
		options = append(options, remote.WithAuth(auth))
	}

	tags, err := remote.List(ref.Context(), append(options, remote.WithContext(h.ctx))...)
	if err != nil {
		kstatus.SetError(image, err.Error())
		return status, err
	}

	status.LastScanTime = metav1.NewTime(time.Now())

	latestTag, err := latestTag(image.Spec.Policy, tags)
	if err != nil {
		kstatus.SetError(image, err.Error())
		return status, err
	}

	status.LatestTag = latestTag
	status.LatestImage = status.CanonicalImageName + ":" + latestTag
	digest, err := getDigest(status.LatestImage, options...)
	if err != nil {
		kstatus.SetError(image, err.Error())
		return status, err
	}
	status.LatestDigest = digest

	interval := image.Spec.Interval
	if interval.Seconds() == 0.0 {
		interval = metav1.Duration{
			Duration: defaultInterval,
		}
	}
	h.imagescans.EnqueueAfter(image.Namespace, image.Name, interval.Duration)
	return status, nil
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

func (h handler) onChangeGitRepo(gitrepo *v1alpha1.GitRepo, status v1alpha1.GitRepoStatus) (v1alpha1.GitRepoStatus, error) {
	if gitrepo == nil || gitrepo.DeletionTimestamp != nil {
		return status, nil
	}
	logrus.Debugf("onChangeGitRepo: gitrepo %s/%s changed, checking for image scans", gitrepo.Namespace, gitrepo.Name)

	imagescans, err := h.imagescans.Cache().List(gitrepo.Namespace, labels.Everything())
	if err != nil {
		return status, err
	}

	var scans []*v1alpha1.ImageScan
	for _, scan := range imagescans {
		if scan.Spec.GitRepoName == gitrepo.Name {
			scans = append(scans, scan)
		}
	}

	if len(scans) == 0 {
		return status, nil
	}

	isStalled := false
	var messages []string
	sort.Slice(scans, func(i, j int) bool {
		return scans[i].Spec.TagName < scans[j].Spec.TagName
	})
	for _, scan := range scans {
		if condition.Cond(imageScanCond).IsFalse(scan) {
			isStalled = true
			messages = append(messages, fmt.Sprintf("imageScan %s is not ready: %s", scan.Spec.TagName, condition.Cond(imageScanCond).GetMessage(scan)))
		}
	}
	if isStalled {
		return status, errors.New(strings.Join(messages, ";"))
	}

	if !shouldSync(gitrepo) {
		return status, nil
	}

	logrus.Debugf("onChangeGitRepo: gitrepo %s/%s changed, syncing repo for image scans", gitrepo.Namespace, gitrepo.Name)

	lock.Lock()
	defer lock.Unlock()
	// todo: maybe we should preserve the dir
	tmp, err := os.MkdirTemp("", fmt.Sprintf("%s-%s", gitrepo.Namespace, gitrepo.Name))
	if err != nil {
		kstatus.SetError(gitrepo, err.Error())
		return status, err
	}
	defer os.RemoveAll(tmp)

	auth, err := h.auth(gitrepo)
	if err != nil {
		kstatus.SetError(gitrepo, err.Error())
		return status, err
	}

	repo, err := gogit.PlainClone(tmp, false, &gogit.CloneOptions{
		URL:           gitrepo.Spec.Repo,
		Auth:          auth,
		RemoteName:    "origin",
		ReferenceName: plumbing.NewBranchReferenceName(gitrepo.Spec.Branch),
		SingleBranch:  true,
		Depth:         1,
		Progress:      nil,
		Tags:          gogit.NoTags,
	})
	if err != nil {
		kstatus.SetError(gitrepo, err.Error())
		return status, err
	}

	// Checking if paths field is empty
	// if yes, using the default value "/"
	paths := gitrepo.Spec.Paths
	if len(paths) == 0 {
		paths = []string{"/"}
	}

	for _, path := range paths {
		updatePath := filepath.Join(tmp, path)
		if err := update.WithSetters(updatePath, updatePath, scans); err != nil {
			kstatus.SetError(gitrepo, err.Error())
			return status, err
		}
	}

	commit, err := commitAllAndPush(context.Background(), repo, auth, gitrepo.Spec.ImageScanCommit)
	if err != nil {
		kstatus.SetError(gitrepo, err.Error())
		return status, err
	}
	if commit != "" {
		logrus.Infof("Repo %s, commit %s pushed", gitrepo.Spec.Repo, commit)
	}
	interval := gitrepo.Spec.ImageSyncInterval
	if interval == nil || interval.Seconds() == 0.0 {
		interval = &metav1.Duration{
			Duration: defaultInterval,
		}
	}
	status.LastSyncedImageScanTime = metav1.NewTime(time.Now())
	h.gitrepos.EnqueueAfter(gitrepo.Namespace, gitrepo.Name, interval.Duration)
	return status, err
}

func shouldSync(gitrepo *v1alpha1.GitRepo) bool {
	interval := gitrepo.Spec.ImageSyncInterval
	if interval == nil || interval.Seconds() == 0.0 {
		interval = &metav1.Duration{
			Duration: defaultInterval,
		}
	}

	if time.Since(gitrepo.Status.LastSyncedImageScanTime.Time) < interval.Duration {
		return false
	}
	return true
}

func commitAllAndPush(ctx context.Context, repo *gogit.Repository, auth transport.AuthMethod, commit v1alpha1.CommitSpec) (string, error) {
	working, err := repo.Worktree()
	if err != nil {
		return "", err
	}

	status, err := working.Status()
	if err != nil {
		return "", err
	} else if status.IsClean() {
		return "", nil
	}

	msgTmpl := commit.MessageTemplate
	if msgTmpl == "" {
		msgTmpl = defaultMessageTemplate
	}
	tmpl, err := template.New("commit message").Parse(msgTmpl)
	if err != nil {
		return "", err
	}
	buf := &strings.Builder{}
	if err := tmpl.Execute(buf, "no data! yet"); err != nil {
		return "", err
	}

	var rev plumbing.Hash
	if rev, err = working.Commit(buf.String(), &gogit.CommitOptions{
		All: true,
		Author: &object.Signature{
			Name:  commit.AuthorName,
			Email: commit.AuthorEmail,
			When:  time.Now(),
		},
	}); err != nil {
		return "", err
	}

	return rev.String(), repo.PushContext(ctx, &gogit.PushOptions{
		Auth: auth,
	})
}

func (h handler) auth(gitrepo *v1alpha1.GitRepo) (transport.AuthMethod, error) {
	if gitrepo.Spec.ClientSecretName == "" {
		return nil, errors.New("requires git secret for write access")
	}

	secret, err := h.secretCache.Get(gitrepo.Namespace, gitrepo.Spec.ClientSecretName)
	if err != nil {
		return nil, err
	}

	switch secret.Type {
	case corev1.SecretTypeBasicAuth:
		return &http.BasicAuth{
			Username: string(secret.Data[corev1.BasicAuthUsernameKey]),
			Password: string(secret.Data[corev1.BasicAuthPasswordKey]),
		}, nil
	case corev1.SecretTypeSSHAuth:
		publicKey, err := ssh.NewPublicKeys("git", secret.Data[corev1.SSHAuthPrivateKey], "")
		if err != nil {
			return nil, err
		}
		return publicKey, nil
	}
	return nil, errors.New("invalid secret type")
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

func shouldScan(image *v1alpha1.ImageScan) bool {
	interval := image.Spec.Interval
	if interval.Seconds() == 0.0 {
		interval = metav1.Duration{
			Duration: defaultInterval,
		}
	}
	if image.Status.LatestTag == "" {
		return true
	}

	if time.Since(image.Status.LastScanTime.Time) < interval.Duration {
		return false
	}
	return true
}

func latestTag(policy v1alpha1.ImagePolicyChoice, versions []string) (string, error) {
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
			des = policy.Alphabetical.Order == AlphabeticalOrderDesc
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
	contraints, err := semver.NewConstraint(r)
	if err != nil {
		return "", err
	}
	var latestVersion *semver.Version
	for _, version := range versions {
		if ver, err := semver.NewVersion(version); err == nil {
			if latestVersion == nil || ver.GreaterThan(latestVersion) {
				if contraints.Check(ver) {
					latestVersion = ver
				}
			}
		}
	}
	return latestVersion.Original(), nil
}
