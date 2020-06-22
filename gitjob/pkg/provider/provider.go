package provider

import (
	"context"

	gitopsv1 "github.com/rancher/gitjobs/pkg/apis/gitops.cattle.io/v1"
)

type Provider interface {
	Supports(obj *gitopsv1.GitJob) bool
	Handle(ctx context.Context, obj *gitopsv1.GitJob) (gitopsv1.GitJobStatus, error)
}
