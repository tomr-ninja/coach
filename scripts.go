package coach

import (
	"context"
	"fmt"

	"github.com/tomr-ninja/coach/docker"
)

func ListScripts(ctx context.Context, modelImage string) ([]string, error) {
	if err := ValidateModelImage(modelImage); err != nil {
		return nil, fmt.Errorf("validate model image: %w", err)
	}

	client, err := docker.NewClient()
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	return client.ListScripts(ctx, modelImage)
}

func RunScript(ctx context.Context, modelImage string, script string, args []string, dataDir string, outputDir string) error {
	if err := ValidateModelImage(modelImage); err != nil {
		return fmt.Errorf("validate model image: %w", err)
	}
	if err := ValidateScriptName(script); err != nil {
		return fmt.Errorf("validate script name: %w", err)
	}
	if err := ValidateDataPath(dataDir); err != nil {
		return fmt.Errorf("validate data path: %w", err)
	}
	if err := ValidateOutputDir(outputDir); err != nil {
		return fmt.Errorf("validate output dir: %w", err)
	}

	client, err := docker.NewClient()
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	return client.RunScript(ctx, modelImage, script, args, dataDir, outputDir)
}
