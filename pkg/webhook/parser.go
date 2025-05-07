package webhook

import (
	"fmt"
	"net/http"

	"github.com/go-playground/webhooks/v6/azuredevops"
	"github.com/go-playground/webhooks/v6/bitbucket"
	bitbucketserver "github.com/go-playground/webhooks/v6/bitbucket-server"
	"github.com/go-playground/webhooks/v6/github"
	"github.com/go-playground/webhooks/v6/gitlab"
	"github.com/go-playground/webhooks/v6/gogs"
	corev1 "k8s.io/api/core/v1"
)

const (
	githubKey          = "github"
	gitlabKey          = "gitlab"
	bitbucketKey       = "bitbucket"
	bitbucketServerKey = "bitbucket-server"
	gogsKey            = "gogs"
	azureUsername      = "azure-username"
	azurePassword      = "azure-password"
)

func parseWebhook(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	switch {
	//Gogs needs to be checked before Github since it carries both Gogs and (incompatible) Github headers
	case r.Header.Get("X-Gogs-Event") != "":
		return parseGogs(r, secret)
	case r.Header.Get("X-GitHub-Event") != "":
		return parseGithub(r, secret)
	case r.Header.Get("X-Gitlab-Event") != "":
		return parseGitlab(r, secret)
	case r.Header.Get("X-Hook-UUID") != "":
		return parseBitbucket(r, secret)
	case r.Header.Get("X-Event-Key") != "":
		return parseBitbucketServer(r, secret)
	case r.Header.Get("X-Vss-Activityid") != "" || r.Header.Get("X-Vss-Subscriptionid") != "":
		return parseAzureDevops(r, secret)
	}

	return nil, nil
}

func getValue(secret *corev1.Secret, key string) (string, error) {
	if secret == nil {
		return "", fmt.Errorf("secret is nil")
	}

	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret key %q not found in secret %q", key, secret.Name)
	}

	return string(value), nil
}

func parseGogs(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	var hook *gogs.Webhook
	var err error

	if secret != nil {
		var value string
		value, err = getValue(secret, gogsKey)
		if err != nil {
			return nil, err
		}
		hook, err = gogs.New(gogs.Options.Secret(value))
	} else {
		hook, err = gogs.New()
	}

	if err != nil {
		return nil, err
	}

	return hook.Parse(r, gogs.PushEvent)
}

func parseGithub(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	var hook *github.Webhook
	var err error

	if secret != nil {
		var value string
		value, err := getValue(secret, githubKey)
		if err != nil {
			return nil, err
		}
		hook, err = github.New(github.Options.Secret(value))
		if err != nil {
			return nil, err
		}
	} else {
		hook, err = github.New()
		if err != nil {
			return nil, err
		}
	}

	return hook.Parse(r, github.PushEvent, github.PingEvent)
}

func parseGitlab(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	var hook *gitlab.Webhook
	var err error

	if secret != nil {
		var value string
		value, err = getValue(secret, gitlabKey)
		if err != nil {
			return nil, err
		}
		hook, err = gitlab.New(gitlab.Options.Secret(value))
	} else {
		hook, err = gitlab.New()
	}

	if err != nil {
		return nil, err
	}

	return hook.Parse(r, gitlab.PushEvents, gitlab.TagEvents)
}

func parseBitbucket(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	var hook *bitbucket.Webhook
	var err error

	if secret != nil {
		var value string
		value, err = getValue(secret, bitbucketKey)
		if err != nil {
			return nil, err
		}
		hook, err = bitbucket.New(bitbucket.Options.UUID(value))
	} else {
		hook, err = bitbucket.New()
	}

	if err != nil {
		return nil, err
	}

	return hook.Parse(r, bitbucket.RepoPushEvent)
}

func parseBitbucketServer(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	var hook *bitbucketserver.Webhook
	var err error

	if secret != nil {
		var value string
		value, err = getValue(secret, bitbucketServerKey)
		if err != nil {
			return nil, err
		}
		hook, err = bitbucketserver.New(bitbucketserver.Options.Secret(value))
	} else {
		hook, err = bitbucketserver.New()
	}

	if err != nil {
		return nil, err
	}

	return hook.Parse(r, bitbucketserver.RepositoryReferenceChangedEvent)
}

func parseAzureDevops(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	var hook *azuredevops.Webhook
	var err error

	if secret != nil {
		var username, token string
		username, err = getValue(secret, azureUsername)
		if err != nil {
			return nil, err
		}

		token, err = getValue(secret, azurePassword)
		if err != nil {
			return nil, err
		}

		hook, err = azuredevops.New(azuredevops.Options.BasicAuth(username, token))
	} else {
		hook, err = azuredevops.New()
	}

	if err != nil {
		return nil, err
	}

	return hook.Parse(r, azuredevops.GitPushEventType)
}
