package coach

import (
	"fmt"

	"github.com/tomr-ninja/coach/docker"
)

func ListScripts(modelImage string) ([]string, error) {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	return client.ListScripts(modelImage)
}

func RunScript(modelImage string, script string, args []string) error {
	client, err := docker.NewRealDockerClient()
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	return client.RunScript(modelImage, script, args)
}
