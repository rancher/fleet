package webhook

import (
	"context"
	"fmt"
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

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ktypes "k8s.io/apimachinery/pkg/types"
	kcache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	webhookSecretName          = "gitjob-webhook" //nolint:gosec // this is a resource name
	webhookDefaultSyncInterval = 3600
	githubKey                  = "github"
	gitlabKey                  = "gitlab"
	bitbucketKey               = "bitbucket"
	bitbucketServerKey         = "bitbucket-server"
	gogsKey                    = "gogs"
	azureUsername              = "azure-username"
	azurePassword              = "azure-password"

	branchRefPrefix = "refs/heads/"
	tagRefPrefix    = "refs/tags/"
)

type Webhook struct {
	client          client.Client
	namespace       string
	github          *github.Webhook
	gitlab          *gitlab.Webhook
	bitbucket       *bitbucket.Webhook
	bitbucketServer *bitbucketserver.Webhook
	gogs            *gogs.Webhook
	log             logr.Logger
	azureDevops     *azuredevops.Webhook
}

func New(namespace string, client client.Client) (*Webhook, error) {
	webhook := &Webhook{
		client:    client,
		namespace: namespace,
		log:       ctrl.Log.WithName("webhook"),
	}
	err := webhook.initGitProviders()
	if err != nil {
		return nil, err
	}

	return webhook, nil
}

func (w *Webhook) initGitProviders() error {
	var err error

	w.github, err = github.New()
	if err != nil {
		return err
	}
	w.gitlab, err = gitlab.New()
	if err != nil {
		return err
	}
	w.bitbucket, err = bitbucket.New()
	if err != nil {
		return err
	}
	w.bitbucketServer, err = bitbucketserver.New()
	if err != nil {
		return err
	}
	w.gogs, err = gogs.New()
	if err != nil {
		return err
	}
	w.azureDevops, err = azuredevops.New()
	if err != nil {
		return err
	}

	return nil
}

func (w *Webhook) onSecretChange(obj interface{}) error {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return fmt.Errorf("expected secret object but got %T", obj)
	}
	if secret.Name != webhookSecretName && secret.Namespace != w.namespace {
		return nil
	}

	var err error
	github, err := github.New(github.Options.Secret(string(secret.Data[githubKey])))
	if err != nil {
		return err
	}
	w.github = github
	gitlab, err := gitlab.New(gitlab.Options.Secret(string(secret.Data[gitlabKey])))
	if err != nil {
		return err
	}
	w.gitlab = gitlab
	bitbucket, err := bitbucket.New(bitbucket.Options.UUID(string(secret.Data[bitbucketKey])))
	if err != nil {
		return err
	}
	w.bitbucket = bitbucket
	bitbucketServer, err := bitbucketserver.New(bitbucketserver.Options.Secret(string(secret.Data[bitbucketServerKey])))
	if err != nil {
		return err
	}
	w.bitbucketServer = bitbucketServer
	gogs, err := gogs.New(gogs.Options.Secret(string(secret.Data[gogsKey])))
	if err != nil {
		return err
	}
	w.gogs = gogs
	azureDevops, err := azuredevops.New(azuredevops.Options.BasicAuth(string(secret.Data[azureUsername]), string(secret.Data[azurePassword])))
	if err != nil {
		return err
	}
	w.azureDevops = azureDevops

	return nil
}

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// credit from https://github.com/argoproj/argo-cd/blob/97003caebcaafe1683e71934eb483a88026a4c33/util/webhook/webhook.go#L327-L350
	var payload interface{}
	var err error
	ctx := r.Context()

	switch {
	//Gogs needs to be checked before Github since it carries both Gogs and (incompatible) Github headers
	case r.Header.Get("X-Gogs-Event") != "":
		payload, err = w.gogs.Parse(r, gogs.PushEvent)
	case r.Header.Get("X-Github-Event") == "ping":
		_, _ = rw.Write([]byte("Webhook received successfully"))
		return
	case r.Header.Get("X-GitHub-Event") != "":
		payload, err = w.github.Parse(r, github.PushEvent)
	case r.Header.Get("X-Gitlab-Event") != "":
		payload, err = w.gitlab.Parse(r, gitlab.PushEvents, gitlab.TagEvents)
	case r.Header.Get("X-Hook-UUID") != "":
		payload, err = w.bitbucket.Parse(r, bitbucket.RepoPushEvent)
	case r.Header.Get("X-Event-Key") != "":
		payload, err = w.bitbucketServer.Parse(r, bitbucketserver.RepositoryReferenceChangedEvent)
	case r.Header.Get("X-Vss-Activityid") != "" || r.Header.Get("X-Vss-Subscriptionid") != "":
		payload, err = w.azureDevops.Parse(r, azuredevops.GitPushEventType)
	default:
		w.log.V(1).Info("Ignoring unknown webhook event")
		return
	}

	w.log.V(1).Info("Webhook payload", "payload", payload)

	if err != nil {
		w.logAndReturn(rw, err)
		return
	}

	revision, branch, _, repoURLs := parsePayload(payload)

	var gitRepoList v1alpha1.GitRepoList
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

			if gitrepo.Status.Commit != revision && revision != "" {
				if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					var gitRepoFromCluster v1alpha1.GitRepo
					err := w.client.Get(
						ctx,
						ktypes.NamespacedName{
							Name:      gitrepo.Name,
							Namespace: gitrepo.Namespace,
						}, &gitRepoFromCluster,
					)
					if err != nil {
						return err
					}
					gitRepoFromCluster.Status.Commit = revision
					// if PollingInterval is not set and webhook is configured, set it to 1 hour
					if gitrepo.Spec.PollingInterval == nil {
						gitRepoFromCluster.Spec.PollingInterval = &metav1.Duration{
							Duration: webhookDefaultSyncInterval * time.Second,
						}
					}
					return w.client.Status().Update(ctx, &gitRepoFromCluster)
				}); err != nil {
					w.logAndReturn(rw, err)
					return
				}
			}
		}
	}
	rw.WriteHeader(200)
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

	var secret corev1.Secret
	informer, err := clientCache.GetInformer(ctx, &secret)
	if err != nil {
		return nil, err
	}

	_, err = informer.AddEventHandler(kcache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			err := webhook.onSecretChange(obj)
			if err != nil {
				webhook.log.Error(err, "new secret added")
			}
		},
		DeleteFunc: func(obj interface{}) {
			err := webhook.initGitProviders()
			if err != nil {
				webhook.log.Error(err, "secret deleted")
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			err := webhook.onSecretChange(newObj)
			if err != nil {
				webhook.log.Error(err, "secret updated")
			}
		},
	})
	if err != nil {
		return nil, err
	}

	return root, nil
}

func (w *Webhook) logAndReturn(rw http.ResponseWriter, err error) {
	w.log.Error(err, "Webhook processing failed")
	rw.WriteHeader(500)
	_, _ = rw.Write([]byte(err.Error()))
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
		if change.New.Type == "branch" {
			branch = change.New.Name
		} else if change.New.Type == "tag" {
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
