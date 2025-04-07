package webhook

import (
	"fmt"
	"net/http"

	"github.com/go-playground/webhooks/v6/azuredevops"
	"gopkg.in/go-playground/webhooks.v5/bitbucket"
	bitbucketserver "gopkg.in/go-playground/webhooks.v5/bitbucket-server"
	"gopkg.in/go-playground/webhooks.v5/github"
	"gopkg.in/go-playground/webhooks.v5/gitlab"
	"gopkg.in/go-playground/webhooks.v5/gogs"
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

func getValue(secret corev1.Secret, key string) (string, error) {
	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("secret key %q not found in secret %q", key, secret.Name)
	}

	return string(value), nil
}

func parseGogs(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	opts := []gogs.Option{}
	if secret != nil {
		value, error := getValue(*secret, gogsKey)
		if error != nil {
			return nil, error
		}
		opts = append(opts, gogs.Options.Secret(value))
	}
	p, err := gogs.New(opts...)
	if err != nil {
		return nil, err
	}

	return p.Parse(r, gogs.PushEvent)
}

func parseGithub(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	opts := []github.Option{}
	if secret != nil {
		value, error := getValue(*secret, githubKey)
		if error != nil {
			return nil, error
		}
		opts = append(opts, github.Options.Secret(value))
	}
	p, err := github.New(opts...)
	if err != nil {
		return nil, err
	}

	return p.Parse(r, github.PushEvent)
}

func parseGitlab(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	opts := []gitlab.Option{}
	if secret != nil {
		value, error := getValue(*secret, gitlabKey)
		if error != nil {
			return nil, error
		}
		opts = append(opts, gitlab.Options.Secret(value))
	}
	p, err := gitlab.New(opts...)
	if err != nil {
		return nil, err
	}

	return p.Parse(r, gitlab.PushEvents, gitlab.TagEvents)
}

func parseBitbucket(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	opts := []bitbucket.Option{}
	if secret != nil {
		value, error := getValue(*secret, bitbucketKey)
		if error != nil {
			return nil, error
		}
		opts = append(opts, bitbucket.Options.UUID(value))
	}
	p, err := bitbucket.New(opts...)
	if err != nil {
		return nil, err
	}

	return p.Parse(r, bitbucket.RepoPushEvent)
}

func parseBitbucketServer(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	opts := []bitbucketserver.Option{}
	if secret != nil {
		value, error := getValue(*secret, bitbucketServerKey)
		if error != nil {
			return nil, error
		}
		opts = append(opts, bitbucketserver.Options.Secret(value))
	}
	p, err := bitbucketserver.New(opts...)
	if err != nil {
		return nil, err
	}

	return p.Parse(r, bitbucketserver.RepositoryReferenceChangedEvent)
}

func parseAzureDevops(r *http.Request, secret *corev1.Secret) (interface{}, error) {
	opts := []azuredevops.Option{}
	if secret != nil {
		username, error := getValue(*secret, azureUsername)
		if error != nil {
			return nil, error
		}
		password, error := getValue(*secret, azurePassword)
		if error != nil {
			return nil, error
		}
		opts = append(opts, azuredevops.Options.BasicAuth(username, password))
	}
	p, err := azuredevops.New(opts...)
	if err != nil {
		return nil, err
	}

	return p.Parse(r, azuredevops.GitPushEventType)
}
