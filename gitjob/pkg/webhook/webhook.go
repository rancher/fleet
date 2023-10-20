package webhook

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	gogsclient "github.com/gogits/go-gogs-client"
	"github.com/gorilla/mux"
	v1controller "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/types"
	corev1controller "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/sirupsen/logrus"
	"gopkg.in/go-playground/webhooks.v5/bitbucket"
	bitbucketserver "gopkg.in/go-playground/webhooks.v5/bitbucket-server"
	"gopkg.in/go-playground/webhooks.v5/github"
	"gopkg.in/go-playground/webhooks.v5/gitlab"
	"gopkg.in/go-playground/webhooks.v5/gogs"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	webhookSecretName = "gitjob-webhook" //nolint:gosec // this is a resource name

	githubKey          = "github"
	gitlabKey          = "gitlab"
	bitbucketKey       = "bitbucket"
	bitbucketServerKey = "bitbucket-server"
	gogsKey            = "gogs"

	branchRefPrefix = "refs/heads/"
	tagRefPrefix    = "refs/tags/"
)

type Webhook struct {
	gitjobs   v1controller.GitJobController
	secrets   corev1controller.SecretController
	namespace string

	github          *github.Webhook
	gitlab          *gitlab.Webhook
	bitbucket       *bitbucket.Webhook
	bitbucketServer *bitbucketserver.Webhook
	gogs            *gogs.Webhook
}

func New(ctx context.Context, rContext *types.Context) *Webhook {
	webhook := &Webhook{
		gitjobs:   rContext.Gitjob.Gitjob().V1().GitJob(),
		secrets:   rContext.Core.Core().V1().Secret(),
		namespace: rContext.Namespace,
	}

	rContext.Core.Core().V1().Secret().OnChange(ctx, "webhook-secret", webhook.onSecretChange)
	return webhook
}

func (w *Webhook) onSecretChange(_ string, secret *corev1.Secret) (*corev1.Secret, error) {
	if secret == nil || secret.DeletionTimestamp != nil {
		return nil, nil
	}

	if secret.Name != webhookSecretName && secret.Namespace != w.namespace {
		return nil, nil
	}

	var err error
	w.github, err = github.New(github.Options.Secret(string(secret.Data[githubKey])))
	if err != nil {
		return nil, err
	}
	w.gitlab, err = gitlab.New(gitlab.Options.Secret(string(secret.Data[gitlabKey])))
	if err != nil {
		return nil, err
	}
	w.bitbucket, err = bitbucket.New(bitbucket.Options.UUID(string(secret.Data[bitbucketKey])))
	if err != nil {
		return nil, err
	}
	w.bitbucketServer, err = bitbucketserver.New(bitbucketserver.Options.Secret(string(secret.Data[bitbucketServerKey])))
	if err != nil {
		return nil, err
	}
	w.gogs, err = gogs.New(gogs.Options.Secret(string(secret.Data[gogsKey])))
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// credit from https://github.com/argoproj/argo-cd/blob/97003caebcaafe1683e71934eb483a88026a4c33/util/webhook/webhook.go#L327-L350
	var payload interface{}
	var err error

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
	}

	gitjobs, err := w.gitjobs.Cache().List("", labels.Everything())
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
		for _, gitjob := range gitjobs {
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

			dp := gitjob.DeepCopy()
			if dp.Status.Commit != revision && revision != "" {
				dp.Status.Commit = revision
				newObj, err := w.gitjobs.UpdateStatus(dp)
				if err != nil {
					logAndReturn(rw, err)
					return
				}
				// if syncInterval is not set and webhook is configured, set it to 1 hour
				if newObj.Spec.SyncInterval == 0 {
					newObj.Spec.SyncInterval = 3600
					if _, err := w.gitjobs.Update(newObj); err != nil {
						logAndReturn(rw, err)
						return
					}
				}
			}
		}
	}
	rw.WriteHeader(200)
	rw.Write([]byte("succeeded"))
}

func logAndReturn(rw http.ResponseWriter, err error) {
	logrus.Errorf("Webhook processing failed: %s", err)
	rw.WriteHeader(500)
	rw.Write([]byte(err.Error()))
	return
}

func HandleHooks(ctx context.Context, rContext *types.Context) http.Handler {
	root := mux.NewRouter()
	webhook := New(ctx, rContext)
	root.UseEncodedPath()
	root.Handle("/", webhook)
	return root
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
