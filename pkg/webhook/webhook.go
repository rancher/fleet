package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-playground/webhooks/v6/azuredevops"
	gogsclient "github.com/gogits/go-gogs-client"
	"github.com/gorilla/mux"
	"gopkg.in/go-playground/webhooks.v5/bitbucket"
	bitbucketserver "gopkg.in/go-playground/webhooks.v5/bitbucket-server"
	"gopkg.in/go-playground/webhooks.v5/github"
	"gopkg.in/go-playground/webhooks.v5/gitlab"
	"gopkg.in/go-playground/webhooks.v5/gogs"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	webhookSecretName          = "gitjob-webhook" //nolint:gosec // this is a resource name
	webhookDefaultSyncInterval = 3600

	branchRefPrefix = "refs/heads/"
	tagRefPrefix    = "refs/tags/"
)

type Webhook struct {
	client    client.Client
	namespace string
	log       logr.Logger
}

func New(namespace string, client client.Client) (*Webhook, error) {
	webhook := &Webhook{
		client:    client,
		namespace: namespace,
		log:       ctrl.Log.WithName("webhook"),
	}

	return webhook, nil
}

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// credit from https://github.com/argoproj/argo-cd/blob/97003caebcaafe1683e71934eb483a88026a4c33/util/webhook/webhook.go#L327-L350
	var payload interface{}
	var err error
	ctx := r.Context()

	// copy the body of the request because we need to parse it twice if secrets are defined
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.logAndReturn(rw, err)
		return
	}

	switch {
	case r.Header.Get("X-Github-Event") == "ping":
		_, _ = rw.Write([]byte("Webhook received successfully"))
		return
	default:
		r.Body = io.NopCloser(bytes.NewBuffer(body))
		payload, err = parseWebhook(r, nil)
		if payload == nil && err == nil {
			w.log.V(1).Info("Ignoring unknown webhook event")
			return
		}
	}

	w.log.V(1).Info("Webhook payload", "payload", payload)

	if err != nil {
		w.logAndReturn(rw, err)
		return
	}

	revision, branch, _, repoURLs := parsePayload(payload)

	var gitRepoList fleet.GitRepoList
	err = w.client.List(ctx, &gitRepoList, &client.ListOptions{LabelSelector: labels.Everything()})
	if err != nil {
		w.logAndReturn(rw, err)
		return
	}

	for _, repo := range repoURLs {
		u, err := url.Parse(repo)
		if err != nil {
			w.logAndReturn(rw, err)
			return
		}
		path := strings.Replace(u.Path[1:], "/_git/", "(/_git)?/", 1)
		regexpStr := `(?i)(http://|https://|\w+@|ssh://(\w+@)?|git@(ssh\.)?)` + u.Hostname() +
			"(:[0-9]+|)[:/](v\\d/)?" + path + "(\\.git)?"
		repoRegexp, err := regexp.Compile(regexpStr)
		if err != nil {
			w.logAndReturn(rw, err)
			return
		}
		for _, gitrepo := range gitRepoList.Items {
			if gitrepo.Spec.Revision != "" {
				continue
			}

			if !repoRegexp.MatchString(gitrepo.Spec.Repo) {
				continue
			}

			if gitrepo.Spec.Branch != "" {
				// we check if the branch from webhook matches gitrepo's branch
				if branch == "" || branch != gitrepo.Spec.Branch {
					continue
				}
			}

			if gitrepo.Status.WebhookCommit != revision && revision != "" {
				// before updating the gitrepo check if a secret was
				// defined and, if so, verify that it is correct
				secret, err := w.getSecret(ctx, gitrepo)
				if err != nil {
					w.logAndReturn(rw, err)
					return
				}
				if secret != nil {
					// At this point we know that a secret is defined and exists.
					// Parse the request again (this time with secret)
					// We need to parse twice because in the first parsing we didn't
					// know the gitrepo associated with the webhook payload.
					// The first parsing is used to get the gitrepo and, if a secret is
					// defined in the gitrepo, it takes precedence over the global one.
					r.Body = io.NopCloser(bytes.NewBuffer(body))
					_, err = parseWebhook(r, secret)
					if err != nil {
						w.logAndReturn(rw, err)
						return
					}
				}

				var gitRepoFromCluster fleet.GitRepo
				err = w.client.Get(
					ctx,
					types.NamespacedName{
						Name:      gitrepo.Name,
						Namespace: gitrepo.Namespace,
					}, &gitRepoFromCluster,
				)
				if err != nil {
					w.logAndReturn(rw, err)
					return
				}
				orig := gitRepoFromCluster.DeepCopy()
				gitRepoFromCluster.Status.WebhookCommit = revision
				// if PollingInterval is not set and webhook is configured, set it to 1 hour
				if gitRepoFromCluster.Spec.PollingInterval == nil {
					gitRepoFromCluster.Spec.PollingInterval = &metav1.Duration{
						Duration: webhookDefaultSyncInterval * time.Second,
					}
				}
				p := client.MergeFrom(orig)
				if err := w.client.Status().Patch(ctx, &gitRepoFromCluster, p); err != nil {
					w.logAndReturn(rw, err)
					return
				}
			}
		}
	}
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write([]byte("succeeded"))
}

func HandleHooks(ctx context.Context, namespace string, client client.Client, clientCache cache.Cache) (http.Handler, error) {
	root := mux.NewRouter()
	webhook, err := New(namespace, client)
	if err != nil {
		return nil, err
	}
	root.UseEncodedPath()
	root.Handle("/", webhook)

	return root, nil
}

func (w *Webhook) logAndReturn(rw http.ResponseWriter, err error) {
	w.log.Error(err, "Webhook processing failed")
	rw.WriteHeader(getErrorCodeFromErr(err))
	_, _ = rw.Write([]byte(err.Error()))
}

func (w *Webhook) getSecret(ctx context.Context, gitrepo fleet.GitRepo) (*corev1.Secret, error) {
	// global secret first (for backward compatibility)
	secretName := webhookSecretName
	ns := w.namespace
	mustExist := false
	if gitrepo.Spec.WebhookSecret != "" {
		// the gitrepo secret takes preference over the global one
		secretName = gitrepo.Spec.WebhookSecret
		ns = gitrepo.Namespace
		mustExist = true // when the secret has been defined in the GitRepo it must exist
	}
	var secret corev1.Secret
	err := w.client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ns}, &secret)
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, err
		}
		if !mustExist {
			return nil, nil
		}
		return nil, fmt.Errorf("secret %q in namespace %q does not exist", secretName, ns)
	}
	return &secret, nil
}

func getErrorCodeFromErr(err error) int {
	// check if the error is a verification of identity error
	// secret check, or basic credentials or token verification
	// depending on the provider
	switch err {
	case
		gogs.ErrHMACVerificationFailed,
		github.ErrHMACVerificationFailed,
		gitlab.ErrGitLabTokenVerificationFailed,
		bitbucket.ErrUUIDVerificationFailed,
		bitbucketserver.ErrHMACVerificationFailed,
		azuredevops.ErrBasicAuthVerificationFailed:

		return http.StatusUnauthorized
	case
		gogs.ErrInvalidHTTPMethod,
		github.ErrInvalidHTTPMethod,
		gitlab.ErrInvalidHTTPMethod,
		bitbucket.ErrInvalidHTTPMethod,
		bitbucketserver.ErrInvalidHTTPMethod,
		azuredevops.ErrInvalidHTTPMethod:

		return http.StatusMethodNotAllowed
	}
	return http.StatusInternalServerError
}

// git ref docs: https://git-scm.com/book/en/v2/Git-Internals-Git-References
func getBranchTagFromRef(ref string) (string, string) {
	if strings.HasPrefix(ref, branchRefPrefix) {
		return strings.TrimPrefix(ref, branchRefPrefix), ""
	}

	if strings.HasPrefix(ref, tagRefPrefix) {
		return "", strings.TrimPrefix(ref, tagRefPrefix)
	}

	return "", ""
}

// parsePayload extracts git information from a request payload, depending on its type.
// Returns a revision, branch, tag and a slice of repo URLs.
func parsePayload(payload interface{}) (revision, branch, tag string, repoURLs []string) {
	// credit from https://github.com/argoproj/argo-cd/blob/97003caebcaafe1683e71934eb483a88026a4c33/util/webhook/webhook.go#L84-L87
	switch t := payload.(type) {
	case github.PushPayload:
		branch, tag = getBranchTagFromRef(t.Ref)
		revision = t.After
		repoURLs = append(repoURLs, t.Repository.HTMLURL)
	case gitlab.PushEventPayload:
		branch, tag = getBranchTagFromRef(t.Ref)
		revision = t.CheckoutSHA
		repoURLs = append(repoURLs, t.Project.WebURL)
	case gitlab.TagEventPayload:
		branch, tag = getBranchTagFromRef(t.Ref)
		revision = t.CheckoutSHA
		repoURLs = append(repoURLs, t.Project.WebURL)
	// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/#Push
	case bitbucket.RepoPushPayload:
		repoURLs = append(repoURLs, t.Repository.Links.HTML.Href)
		if len(t.Push.Changes) == 0 {
			break
		}

		change := t.Push.Changes[0]
		revision = change.New.Target.Hash
		switch change.New.Type {
		case "branch":
			branch = change.New.Name
		case "tag":
			tag = change.New.Name
		}
	case bitbucketserver.RepositoryReferenceChangedPayload:
		for _, l := range t.Repository.Links["clone"].([]interface{}) {
			link := l.(map[string]interface{})
			if link["name"] == "http" {
				repoURLs = append(repoURLs, link["href"].(string))
			}
			if link["name"] == "ssh" {
				repoURLs = append(repoURLs, link["href"].(string))
			}
		}
		for _, change := range t.Changes {
			revision = change.ToHash
			branch, tag = getBranchTagFromRef(change.ReferenceId)
			break
		}
	case gogsclient.PushPayload:
		repoURLs = append(repoURLs, t.Repo.HTMLURL)
		branch, tag = getBranchTagFromRef(t.Ref)
		revision = t.After
	case azuredevops.GitPushEvent:
		repoURLs = append(repoURLs, t.Resource.Repository.RemoteURL)
		for _, refUpdate := range t.Resource.RefUpdates {
			branch, tag = getBranchTagFromRef(refUpdate.Name)
			revision = refUpdate.NewObjectID
			break
		}
	}

	return revision, branch, tag, repoURLs
}
