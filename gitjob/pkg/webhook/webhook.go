package webhook

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	goPlaygroundAzuredevops "github.com/go-playground/webhooks/v6/azuredevops"
	"github.com/rancher/gitjob/pkg/webhook/azuredevops"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	kcache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/Masterminds/semver/v3"
	gogsclient "github.com/gogits/go-gogs-client"
	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/sirupsen/logrus"
	"gopkg.in/go-playground/webhooks.v5/bitbucket"
	bitbucketserver "gopkg.in/go-playground/webhooks.v5/bitbucket-server"
	"gopkg.in/go-playground/webhooks.v5/github"
	"gopkg.in/go-playground/webhooks.v5/gitlab"
	"gopkg.in/go-playground/webhooks.v5/gogs"
	"k8s.io/apimachinery/pkg/labels"
	ktypes "k8s.io/apimachinery/pkg/types"
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
	case r.Header.Get("X-GitHub-Event") != "":
		payload, err = w.github.Parse(r, github.PushEvent)
	case r.Header.Get("X-Gitlab-Event") != "":
		payload, err = w.gitlab.Parse(r, gitlab.PushEvents, gitlab.TagEvents)
	case r.Header.Get("X-Hook-UUID") != "":
		payload, err = w.bitbucket.Parse(r, bitbucket.RepoPushEvent)
	case r.Header.Get("X-Event-Key") != "":
		payload, err = w.bitbucketServer.Parse(r, bitbucketserver.RepositoryReferenceChangedEvent)
	case r.Header.Get("X-Vss-Activityid") != "" || r.Header.Get("X-Vss-Subscriptionid") != "":
		payload, err = w.azureDevops.Parse(r, goPlaygroundAzuredevops.GitPushEventType)
	default:
		logrus.Debug("Ignoring unknown webhook event")
		return
	}

	logrus.Debugf("Webhook payload %+v", payload)

	if err != nil {
		logAndReturn(rw, err)
		return
	}

	var revision, branch, tag string
	var repoURLs []string
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
		for _, change := range t.Push.Changes {
			revision = change.New.Target.Hash
			if change.New.Type == "branch" {
				branch = change.New.Name
			} else if change.New.Type == "tag" {
				tag = change.New.Name
			}
			break
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
	case goPlaygroundAzuredevops.GitPushEvent:
		repoURLs = append(repoURLs, t.Resource.Repository.RemoteURL)
		for _, refUpdate := range t.Resource.RefUpdates {
			branch, tag = getBranchTagFromRef(refUpdate.Name)
			revision = refUpdate.NewObjectID
			break
		}
	}

	var gitJobList v1.GitJobList
	err = w.client.List(ctx, &gitJobList, &client.ListOptions{LabelSelector: labels.Everything()})
	if err != nil {
		logAndReturn(rw, err)
		return
	}

	for _, repo := range repoURLs {
		u, err := url.Parse(repo)
		if err != nil {
			logAndReturn(rw, err)
			return
		}
		regexpStr := `(?i)(http://|https://|\w+@|ssh://(\w+@)?)` + u.Hostname() + "(:[0-9]+|)[:/]" + u.Path[1:] + "(\\.git)?"
		repoRegexp, err := regexp.Compile(regexpStr)
		if err != nil {
			logAndReturn(rw, err)
			return
		}
		for _, gitjob := range gitJobList.Items {
			if gitjob.Spec.Git.Revision != "" {
				continue
			}

			if !repoRegexp.MatchString(gitjob.Spec.Git.Repo) {
				continue
			}

			// if onTag is enabled, we only watch tag event, as it can be coming from any branch
			if gitjob.Spec.Git.OnTag != "" {
				// skipping if gitjob is watching tag only and tag is empty(not a tag event)
				if tag == "" {
					continue
				}
				contraints, err := semver.NewConstraint(gitjob.Spec.Git.OnTag)
				if err != nil {
					logrus.Warnf("Failed to parsing onTag semver from %s/%s, err: %v, skipping", gitjob.Namespace, gitjob.Name, err)
					continue
				}
				v, err := semver.NewVersion(tag)
				if err != nil {
					logrus.Warnf("Failed to parsing semver on incoming tag, err: %v, skipping", err)
					continue
				}
				if !contraints.Check(v) {
					continue
				}
			} else if gitjob.Spec.Git.Branch != "" {
				// else we check if the branch from webhook matches gitjob's branch
				if branch == "" || branch != gitjob.Spec.Git.Branch {
					continue
				}
			}

			if gitjob.Status.Commit != revision && revision != "" {
				if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					var gitJobFomCluster v1.GitJob
					err := w.client.Get(ctx, ktypes.NamespacedName{Name: gitjob.Name, Namespace: gitjob.Namespace}, &gitJobFomCluster)
					if err != nil {
						return err
					}
					gitJobFomCluster.Status.Commit = revision
					// if syncInterval is not set and webhook is configured, set it to 1 hour
					if gitjob.Spec.SyncInterval == 0 {
						gitJobFomCluster.Spec.SyncInterval = webhookDefaultSyncInterval
					}
					return w.client.Status().Update(ctx, &gitJobFomCluster)
				}); err != nil {
					logAndReturn(rw, err)
					return
				}
			}
		}
	}
	rw.WriteHeader(200)
	rw.Write([]byte("succeeded"))
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

func logAndReturn(rw http.ResponseWriter, err error) {
	logrus.Errorf("Webhook processing failed: %s", err)
	rw.WriteHeader(500)
	rw.Write([]byte(err.Error()))
	return
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
