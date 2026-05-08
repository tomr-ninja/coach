package coach

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/protocol"
)

var (
	errMixedLocalS3   = errors.New("data source and output must both be local or both be s3")
	errNoRegistry     = errors.New("registry is required for remote S3 runs (set in coach.json)")
	errImageNotLocal  = errors.New("model image must be available locally; pull it first with docker pull")
	errNoSubmitResult = errors.New("driver returned no submit result")
	errNoListResult   = errors.New("driver returned no list result")
	errNoStatusResult = errors.New("driver returned no status result")
	errEmptySubmitID  = errors.New("submit result has empty ID")
	errEmptyStatusID  = errors.New("status result has empty ID")
	errEmptyState     = errors.New("status result has empty State")
)

func ScheduleCreate(
	ctx context.Context,
	backendName, modelImage, dataSource, outputURI, scheduleCron string,
	command []string,
	script string,
	resources protocol.Resources,
	labels map[string]string,
) (string, error) {
	if err := ValidateModelImage(modelImage); err != nil {
		return "", fmt.Errorf("validate model image: %w", err)
	}
	if err := ValidateDataPath(dataSource); err != nil {
		return "", fmt.Errorf("validate data source: %w", err)
	}
	if err := ValidateOutputDir(outputURI); err != nil {
		return "", fmt.Errorf("validate output destination: %w", err)
	}
	if err := ValidateCron(scheduleCron); err != nil {
		return "", fmt.Errorf("validate schedule: %w", err)
	}
	if script != "" {
		if err := ValidateScriptName(script); err != nil {
			return "", fmt.Errorf("validate script name: %w", err)
		}
	}

	cfg, err := LoadConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return "", err
	}

	if err = ValidateDriver(backend.Driver); err != nil {
		return "", fmt.Errorf("validate driver: %w", err)
	}

	client, err := docker.NewClient()
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	exists, err := client.ImageExists(ctx, modelImage)
	if err != nil {
		return "", fmt.Errorf("check image exists: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("%w: %s", errImageNotLocal, modelImage)
	}

	digest, err := client.EnsureImageDigest(ctx, modelImage)
	if err != nil {
		return "", fmt.Errorf("image digest: %w", err)
	}

	checksums, err := ResolveChecksums(dataSource, cfg)
	if err != nil {
		return "", fmt.Errorf("resolve checksums: %w", err)
	}

	fingerprint := artifactFingerprint(digest, checksums)
	fingerprintHex := fmt.Sprintf("%x", fingerprint)

	wrapped := strings.HasPrefix(dataSource, "s3://")
	if wrapped && !strings.HasPrefix(outputURI, "s3://") {
		return "", fmt.Errorf("%w: s3 data source requires s3 output destination", errMixedLocalS3)
	}
	if !wrapped && strings.HasPrefix(outputURI, "s3://") {
		return "", fmt.Errorf("%w: local data source requires local output destination", errMixedLocalS3)
	}

	var model protocol.Model
	var wrappedImage string
	var wrapErr error

	if wrapped {
		if cfg.Registry == "" {
			return "", fmt.Errorf("%w", errNoRegistry)
		}

		s3PathIn := strings.TrimPrefix(dataSource, "s3://")
		s3PathOut := strings.TrimPrefix(outputURI, "s3://")
		if !strings.HasSuffix(s3PathOut, "/") {
			s3PathOut += "/"
		}
		s3PathOut += fingerprintHex

		envVars := buildS3EnvVars(cfg.S3)
		envVars["S3_PATH_IN"] = s3PathIn
		envVars["S3_PATH_OUT"] = s3PathOut

		entrypoint, _, entryErr := client.ImageEntrypoint(ctx, modelImage)
		if entryErr != nil {
			return "", fmt.Errorf("inspect image entrypoint: %w", entryErr)
		}

		wrappedImage, wrapErr = WrapImage(ctx, client, modelImage, fingerprintHex, cfg.Registry, cfg.RegistryAuth.Reveal(), entrypoint)
		if wrapErr != nil {
			return "", fmt.Errorf("wrap image: %w", wrapErr)
		}

		model = protocol.Model{
			Image:   wrappedImage,
			Command: command,
			Script:  script,
			EnvVars: envVars,
		}
	} else {
		model = protocol.Model{
			Image:   modelImage,
			Command: command,
			Script:  script,
		}
	}

	name := fmt.Sprintf("coach-container-runner-%s", sanitizeImageName(modelImage))
	job := &protocol.Job{
		Fingerprint: fingerprintHex,
		Name:        name,
		IsRecurring: scheduleCron != "",
		IsWrapped:   wrapped,
		Model:       model,
		Data: protocol.Data{
			Sources:   []string{dataSource},
			MountPath: "/data",
		},
		Output: protocol.Output{
			Destination: outputURI,
			MountPath:   "/output",
		},
		Resources: resources,
		Labels:    labels,
	}

	if scheduleCron != "" {
		job.Schedule = &protocol.Schedule{Cron: scheduleCron, Timezone: "UTC"}
	}

	spec := &protocol.Spec{
		ProtocolVersion: protocol.Version,
		Type:            "submit",
		Job:             job,
	}
	result, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config)
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
	if err := ValidateSubmitResult(result.SubmitResult); err != nil {
		return "", fmt.Errorf("validate submit result: %w", err)
	}

	return result.SubmitResult.ID, nil
}

func ScheduleList(ctx context.Context, backendName string) ([]protocol.ScheduleEntry, error) {
	cfg, err := LoadConfig()
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
	result, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config)
	if err != nil {
		return nil, fmt.Errorf("invoke driver: %w", err)
	}

	if result.ListResult == nil {
		return nil, errNoListResult
	}

	return result.ListResult.Entries, nil
}

func ScheduleDelete(ctx context.Context, backendName, id string) error {
	cfg, err := LoadConfig()
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
	if _, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config); err != nil {
		return fmt.Errorf("invoke driver: %w", err)
	}

	return nil
}

func ScheduleStatus(ctx context.Context, backendName, id string) (*protocol.StatusResult, error) {
	cfg, err := LoadConfig()
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
	result, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config)
	if err != nil {
		return nil, fmt.Errorf("invoke driver: %w", err)
	}

	if result.StatusResult == nil {
		return nil, errNoStatusResult
	}
	if err := ValidateStatusResult(result.StatusResult); err != nil {
		return nil, fmt.Errorf("validate status result: %w", err)
	}

	return result.StatusResult, nil
}

func ValidateSubmitResult(r *protocol.SubmitResult) error {
	if r.ID == "" {
		return errEmptySubmitID
	}

	return nil
}

func ValidateStatusResult(r *protocol.StatusResult) error {
	if r.ID == "" {
		return errEmptyStatusID
	}
	if r.State == "" {
		return errEmptyState
	}

	return nil
}
