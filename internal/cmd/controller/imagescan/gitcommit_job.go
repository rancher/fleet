// Copyright (c) 2021-2023 SUSE LLC

package imagescan

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-logr/logr"
	"github.com/reugn/go-quartz/quartz"
	"golang.org/x/sync/semaphore"

	"github.com/rancher/fleet/internal/cmd/controller/imagescan/update"
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
	defaultMessageTemplate = `Update from image update automation`
)

var (
	DefaultInterval = metav1.Duration{Duration: durations.DefaultImageInterval}
	lock            = sync.Mutex{}
)

var _ quartz.Job = &GitCommitJob{}

type GitCommitJob struct {
	sem    *semaphore.Weighted
	client client.Client

	namespace string
	name      string
}

func gitCommitDescription(namespace string, name string) string {
	return fmt.Sprintf("gitrepo-git-commit-%s-%s", namespace, name)
}

func GitCommitKey(namespace string, name string) *quartz.JobKey {
	return quartz.NewJobKey(gitCommitDescription(namespace, name))
}

func NewGitCommitJob(c client.Client, namespace string, name string) *GitCommitJob {
	return &GitCommitJob{
		sem:    semaphore.NewWeighted(1),
		client: c,

		namespace: namespace,
		name:      name,
	}
}

func (j *GitCommitJob) Execute(ctx context.Context) error {
	if !j.sem.TryAcquire(1) {
		// already running
		return nil
	}
	defer j.sem.Release(1)

	j.cloneAndReplace(ctx)

	return nil
}

func (j *GitCommitJob) Description() string {
	return gitCommitDescription(j.namespace, j.name)
}

func (j *GitCommitJob) cloneAndReplace(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("imagescan-clone").WithValues("gitrepo", j.name, "namespace", j.namespace)
	nsn := types.NamespacedName{Namespace: j.namespace, Name: j.name}

	gitrepo := &fleet.GitRepo{}
	if err := j.client.Get(ctx, nsn, gitrepo); err != nil {
		return
	}

	images := &fleet.ImageScanList{}
	if err := j.client.List(ctx, images, client.InNamespace(j.namespace)); err != nil {
		return
	}

	if len(images.Items) == 0 {
		return
	}

	// filter all imagescans in the namespace for this repo (.spec.gitRepoName)
	scans := make([]*fleet.ImageScan, 0, len(images.Items))
	for _, image := range images.Items {
		if image.Spec.GitRepoName == j.name {
			image := image
			scans = append(scans, &image)
		}
	}

	isStalled := false
	var messages []string
	sort.Slice(scans, func(i, j int) bool {
		return scans[i].Spec.TagName < scans[j].Spec.TagName
	})
	c := condition.Cond(fleet.ImageScanScanCondition)
	for _, scan := range scans {
		if c.IsFalse(scan) {
			isStalled = true
			messages = append(messages, fmt.Sprintf("imageScan %s is not ready: %s", scan.Spec.TagName, c.GetMessage(scan)))
		}
	}
	if isStalled {
		err := errors.New(strings.Join(messages, ";"))
		logger.V(1).Info("Image scan is stalled", "error", err)
		return
	}

	if !shouldSync(gitrepo) {
		return
	}

	logger.V(1).Info("Syncing repo for image scans")

	// This lock is required to prevent conflicts while using the environment variable SSH_KNOWN_HOSTS.
	// It was added before the SSH support so there might be other potential conflicts without it.
	lock.Lock()
	defer lock.Unlock()

	// todo: maybe we should preserve the dir
	tmp, err := os.MkdirTemp("", fmt.Sprintf("%s-%s", gitrepo.Namespace, gitrepo.Name))
	if err != nil {
		err = j.updateErrorStatus(ctx, gitrepo, err)
		logger.V(1).Info("Cannot create temp dir to clone repo", "error", err)
		return
	}
	defer os.RemoveAll(tmp)

	auth, err := readAuth(ctx, logger, j.client, gitrepo)
	if err != nil {
		err = j.updateErrorStatus(ctx, gitrepo, err)
		logger.V(1).Info("Cannot create temp dir to clone repo", "error", err)
		return
	}

	// Remove SSH known_hosts tmpdir unless it was provided by the user
	if os.Getenv("SSH_KNOWN_HOSTS") != "" {
		tmpdir := filepath.Dir(os.Getenv("SSH_KNOWN_HOSTS"))
		if strings.HasPrefix(tmpdir, "/tmp/"+fmt.Sprintf("ssh-%s-%s-", gitrepo.Namespace, gitrepo.Name)) {
			defer os.RemoveAll(tmpdir)
		}
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
		err = j.updateErrorStatus(ctx, gitrepo, err)
		logger.V(1).Info("Cannot clone git repo", "error", err)
		return
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
			err = j.updateErrorStatus(ctx, gitrepo, err)
			logger.V(1).Info("Cannot update image tags in repo", "error", err)
			return
		}
	}

	commit, err := commitAllAndPush(context.Background(), repo, auth, gitrepo.Spec.ImageScanCommit)
	if err != nil {
		err = j.updateErrorStatus(ctx, gitrepo, err)
		logger.V(1).Info("Cannot commit and push to repo", "error", err)
		return
	}
	if commit != "" {
		logger.Info("Created commit in repo", "repo", gitrepo.Spec.Repo, "commit", commit)
	}
	interval := gitrepo.Spec.ImageSyncInterval
	if interval == nil || interval.Seconds() == 0.0 {
		interval = &DefaultInterval
	}
	gitrepo.Status.LastSyncedImageScanTime = metav1.NewTime(time.Now())

	// update gitrepo status
	condition.Cond(fleet.ImageScanSyncCondition).SetError(&gitrepo.Status, "", nil)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.GitRepo{}
		err := j.client.Get(ctx, nsn, t)
		if err != nil {
			return err
		}
		t.Status = gitrepo.Status
		return j.client.Status().Update(ctx, t)
	})

	if err != nil {
		logger.Error(err, "Failed to update gitrepo status", "status", gitrepo.Status)
	}
}

func (j *GitCommitJob) updateErrorStatus(ctx context.Context, gitrepo *fleet.GitRepo, orgErr error) error {
	nsn := types.NamespacedName{Name: gitrepo.Name, Namespace: gitrepo.Namespace}

	condition.Cond(fleet.ImageScanSyncCondition).SetError(&gitrepo.Status, "", orgErr)
	kstatus.SetError(gitrepo, orgErr.Error())
	merr := []error{orgErr}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.GitRepo{}
		err := j.client.Get(ctx, nsn, t)
		if err != nil {
			return err
		}
		t.Status = gitrepo.Status
		return j.client.Status().Update(ctx, t)
	})
	if err != nil {
		merr = append(merr, err)
	}
	return errutil.NewAggregate(merr)
}

func shouldSync(gitrepo *fleet.GitRepo) bool {
	interval := gitrepo.Spec.ImageSyncInterval
	if interval == nil || interval.Seconds() == 0.0 {
		interval = &DefaultInterval
	}

	if time.Since(gitrepo.Status.LastSyncedImageScanTime.Time) < interval.Duration {
		return false
	}
	return true
}

func readAuth(ctx context.Context, logger logr.Logger, c client.Client, gitrepo *fleet.GitRepo) (transport.AuthMethod, error) {
	if gitrepo.Spec.ClientSecretName == "" {
		return nil, errors.New("requires git secret for write access")
	}
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Spec.ClientSecretName}, secret)
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
		knownHosts := secret.Data["known_hosts"]
		if knownHosts == nil {
			logger.Info("The git secret does not have a known_hosts field, so no host key verification possible!", "secret", gitrepo.Spec.ClientSecretName)
		} else {
			err := setupKnownHosts(gitrepo, knownHosts)
			if err != nil {
				return nil, err
			}
		}

		publicKey, err := ssh.NewPublicKeys("git", secret.Data[corev1.SSHAuthPrivateKey], "")
		if err != nil {
			return nil, err
		}
		return publicKey, nil
	}
	return nil, errors.New("invalid secret type")
}

func setupKnownHosts(gitrepo *fleet.GitRepo, data []byte) error {
	tmpdir, err := os.MkdirTemp("", fmt.Sprintf("ssh-%s-%s-", gitrepo.Namespace, gitrepo.Name))
	if err != nil {
		return err
	}

	known := path.Join(tmpdir, "known_hosts")
	err = os.Setenv("SSH_KNOWN_HOSTS", known)
	if err != nil {
		return err
	}

	file, err := os.Create(known)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)
	if err != nil {
		return err
	}

	return nil
}

func commitAllAndPush(ctx context.Context, repo *gogit.Repository, auth transport.AuthMethod, commit fleet.CommitSpec) (string, error) {
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
