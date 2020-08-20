package controller

import (
	"context"

	"github.com/rancher/gitjob/pkg/controller/gitjob"
	"github.com/rancher/gitjob/pkg/controller/job"
	"github.com/rancher/gitjob/pkg/types"
)

func Register(ctx context.Context, cont *types.Context) {
	gitjob.Register(ctx, cont)

	job.Register(ctx, cont)
}
