package coach

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/driver"
	"github.com/tomr-ninja/coach/internal/schedule"
	"github.com/tomr-ninja/coach/internal/validate"
	"github.com/tomr-ninja/coach/internal/wrap"
	"github.com/tomr-ninja/coach/protocol"
)

var (
	errImageNotLocal  = errors.New("model image must be available locally; pull it first with docker pull")
	errNoSubmitResult = errors.New("driver returned no submit result")
	errNoListResult   = errors.New("driver returned no list result")
	errNoStatusResult = errors.New("driver returned no status result")
)

func ScheduleCreate(
	ctx context.Context,
	backendName, modelImage, dataSource, outputURI, scheduleCron string,
	command []string,
	script string,
	resources protocol.Resources,
	labels map[string]string,
) (string, error) {
	if err := validate.ModelImage(modelImage); err != nil {
		return "", fmt.Errorf("validate model image: %w", err)
	}
	if err := validate.DirPath(dataSource); err != nil {
		return "", fmt.Errorf("validate data source: %w", err)
	}
	if err := validate.DirPath(outputURI); err != nil {
		return "", fmt.Errorf("validate output destination: %w", err)
	}
	if err := validate.Cron(scheduleCron); err != nil {
		return "", fmt.Errorf("validate schedule: %w", err)
	}
	if script != "" {
		if err := validate.ScriptName(script); err != nil {
			return "", fmt.Errorf("validate script name: %w", err)
		}
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return "", err
	}

	if err = driver.Validate(backend.Driver); err != nil {
		return "", fmt.Errorf("validate driver: %w", err)
	}

	client, err := docker.NewClient()
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	imageDigestHex, err := resolveImageDigestHex(ctx, client, modelImage)
	if err != nil {
		return "", err
	}

	if err := validate.LocalVsS3(dataSource, outputURI); err != nil {
		return "", err
	}

	wrapped := strings.HasPrefix(dataSource, "s3://")
	var model protocol.Model
	var wrappedImage string

	if wrapped {
		model, wrappedImage, err = wrapModelForS3Schedule(ctx, client, modelImage, imageDigestHex, dataSource, outputURI, cfg, command, script, backend.Platform)
		if err != nil {
			return "", err
		}
	} else {
		model = protocol.Model{
			Image:   modelImage,
			Command: command,
			Script:  script,
		}
	}

	job := schedule.BuildJob(schedule.BuildJobParams{
		ImageDigest:  imageDigestHex,
		ModelImage:   modelImage,
		DataSource:   dataSource,
		OutputURI:    outputURI,
		ScheduleCron: scheduleCron,
		Wrapped:      wrapped,
		Model:        model,
		Resources:    resources,
		Labels:       labels,
	})

	spec := &protocol.Spec{
		ProtocolVersion: protocol.Version,
		Type:            "submit",
		Job:             job,
	}
	result, err := driver.InvokeWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration())
	if err != nil {
		if wrappedImage != "" {
			if rmErr := client.ImageRemove(context.Background(), wrappedImage, true); rmErr != nil {
				fmt.Fprintf(os.Stderr, "warning: cleanup wrapper image %s: %v\n", wrappedImage, rmErr)
			}
		}
		return "", fmt.Errorf("invoke driver: %w", err)
	}

	if result.SubmitResult == nil {
		return "", errNoSubmitResult
	}
	if err := validate.SubmitResult(result.SubmitResult); err != nil {
		return "", fmt.Errorf("validate submit result: %w", err)
	}

	return result.SubmitResult.ID, nil
}

// resolveImageDigestHex checks the image exists locally and returns its SHA256 digest as a hex string.
func resolveImageDigestHex(ctx context.Context, client *docker.Client, modelImage string) (string, error) {
	exists, err := client.ImageExists(ctx, modelImage)
	if err != nil {
		return "", fmt.Errorf("check image exists: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("%w: %s", errImageNotLocal, modelImage)
	}

	digest, err := client.EnsureImageDigest(ctx, modelImage, "")
	if err != nil {
		return "", fmt.Errorf("image digest: %w", err)
	}

	return fmt.Sprintf("%x", digest), nil
}

// wrapModelForS3Schedule builds an S3 wrapper image for a scheduled job.
//
// Unlike the one-off Run path, this does NOT pre-compute the artifact fingerprint.
// Instead it passes imageDigestHex and S3_PATH_OUT_PREFIX as env vars. The wrapper
// entrypoint computes the fingerprint from the actual data at execution time and
// appends it to S3_PATH_OUT_PREFIX.
func wrapModelForS3Schedule(
	ctx context.Context,
	client *docker.Client,
	modelImage, imageDigestHex, dataSource, outputURI string,
	cfg *config.Config,
	command []string,
	script string,
	targetPlatform string,
) (protocol.Model, string, error) {
	if cfg.Registry == "" {
		return protocol.Model{}, "", wrap.ErrNoRegistry
	}

	s3PathIn := strings.TrimPrefix(dataSource, "s3://")
	s3PathOutPrefix := strings.TrimPrefix(outputURI, "s3://")
	if !strings.HasSuffix(s3PathOutPrefix, "/") {
		s3PathOutPrefix += "/"
	}

	envVars := s3WrapperEnvVars(cfg, s3PathIn, s3PathOutPrefix, imageDigestHex)

	entrypoint, _, err := client.ImageEntrypoint(ctx, modelImage)
	if err != nil {
		return protocol.Model{}, "", fmt.Errorf("inspect image entrypoint: %w", err)
	}

	wrappedImage, err := wrap.Image(ctx, client, modelImage, imageDigestHex, cfg.Registry, cfg.RegistryAuth.Reveal(), targetPlatform, entrypoint)
	if err != nil {
		return protocol.Model{}, "", fmt.Errorf("wrap image: %w", err)
	}

	model := protocol.Model{
		Image:   wrappedImage,
		Command: command,
		Script:  script,
		EnvVars: envVars,
	}

	return model, wrappedImage, nil
}

func ScheduleList(ctx context.Context, backendName string) ([]protocol.ScheduleEntry, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return nil, err
	}

	spec := &protocol.Spec{
		ProtocolVersion: protocol.Version,
		Type:            "list",
	}
	result, err := driver.InvokeWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration())
	if err != nil {
		return nil, fmt.Errorf("invoke driver: %w", err)
	}

	if result.ListResult == nil {
		return nil, errNoListResult
	}

	return result.ListResult.Entries, nil
}

func ScheduleDelete(ctx context.Context, backendName, id string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return err
	}

	spec := &protocol.Spec{
		ProtocolVersion: protocol.Version,
		Type:            "delete",
		ID:              id,
	}
	if _, err := driver.InvokeWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration()); err != nil {
		return fmt.Errorf("invoke driver: %w", err)
	}

	return nil
}

func ScheduleStatus(ctx context.Context, backendName, id string) (*protocol.StatusResult, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return nil, err
	}

	spec := &protocol.Spec{
		ProtocolVersion: protocol.Version,
		Type:            "status",
		ID:              id,
	}
	result, err := driver.InvokeWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration())
	if err != nil {
		return nil, fmt.Errorf("invoke driver: %w", err)
	}

	if result.StatusResult == nil {
		return nil, errNoStatusResult
	}
	if err := validate.StatusResult(result.StatusResult); err != nil {
		return nil, fmt.Errorf("validate status result: %w", err)
	}

	return result.StatusResult, nil
}
