package controller

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/controllers"

	"k8s.io/client-go/rest"
)

func start(ctx context.Context, systemNamespace string, client *rest.Config, disableGitops bool) error {
	return controllers.Register(ctx, systemNamespace, client, disableGitops)
}
