package provider

import (
	"context"
	"net/http"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
)

const (
	DefaultSecretName = "gitcredential"
)

type Provider interface {
	Supports(obj *gitjobv1.GitJob) bool
	Handle(ctx context.Context, obj *gitjobv1.GitJob) (gitjobv1.GitjobStatus, error)
	HandleHook(ctx context.Context, req *http.Request) (int, error)
}
