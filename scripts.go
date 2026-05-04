package coach

import (
	"context"
	"fmt"

	"github.com/tomr-ninja/coach/docker"
)

func ListScripts(ctx context.Context, modelImage string) ([]string, error) {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	return client.ListScripts(ctx, modelImage)
}

func RunScript(ctx context.Context, modelImage string, script string, args []string) error {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	return client.RunScript(ctx, modelImage, script, args)
}
