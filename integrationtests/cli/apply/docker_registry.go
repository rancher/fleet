package apply

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func init() {
	utils.DisableReaper()
}

func startDockerRegistry(ctx context.Context) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:        "registry:2",
		ExposedPorts: []string{"5000/tcp"},
		WaitingFor:   wait.ForHTTP("/v2/").WithPort("5000/tcp"),
	}

	maxRetries := 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})

		if err == nil {
			return container, nil
		}

		lastErr = err

		if i < maxRetries-1 {
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to start docker registry after %d attempts: %w", maxRetries, lastErr)
	}
	return nil, fmt.Errorf("failed to start docker registry after %d attempts", maxRetries)
}
