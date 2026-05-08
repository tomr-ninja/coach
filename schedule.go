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

	fingerprint, err := resolveScheduleFingerprint(ctx, client, modelImage, dataSource, cfg)
	if err != nil {
		return "", err
	}
	fingerprintHex := fmt.Sprintf("%x", fingerprint)

	if err := validateLocalVsS3(dataSource, outputURI); err != nil {
		return "", err
	}

	wrapped := strings.HasPrefix(dataSource, "s3://")
	var model protocol.Model
	var wrappedImage string

	if wrapped {
		model, wrappedImage, err = wrapModelForS3(ctx, client, modelImage, fingerprintHex, dataSource, outputURI, cfg, command, script)
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

	job := buildScheduleJob(buildScheduleJobParams{
		Fingerprint:  fingerprintHex,
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
	result, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration())
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

// resolveScheduleFingerprint checks the image exists locally, resolves its digest
// and data checksums, then computes the combined artifact fingerprint.
func resolveScheduleFingerprint(ctx context.Context, client *docker.Client, modelImage, dataSource string, cfg *Config) ([32]byte, error) {
	exists, err := client.ImageExists(ctx, modelImage)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("check image exists: %w", err)
	}
	if !exists {
		return zeroFingerprint, fmt.Errorf("%w: %s", errImageNotLocal, modelImage)
	}

	digest, err := client.EnsureImageDigest(ctx, modelImage)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("image digest: %w", err)
	}

	checksums, err := ResolveChecksums(dataSource, cfg)
	if err != nil {
		return zeroFingerprint, fmt.Errorf("resolve checksums: %w", err)
	}

	return artifactFingerprint(digest, checksums), nil
}

// validateLocalVsS3 ensures data source and output are either both local or both S3.
func validateLocalVsS3(dataSource, outputURI string) error {
	dataIsS3 := strings.HasPrefix(dataSource, "s3://")
	outputIsS3 := strings.HasPrefix(outputURI, "s3://")
	if dataIsS3 != outputIsS3 {
		return fmt.Errorf("%w", errMixedLocalS3)
	}
	return nil
}

// wrapModelForS3 builds an S3 wrapper image and returns the wrapped Model plus the image tag.
func wrapModelForS3(
	ctx context.Context,
	client *docker.Client,
	modelImage, fingerprintHex, dataSource, outputURI string,
	cfg *Config,
	command []string,
	script string,
) (protocol.Model, string, error) {
	if cfg.Registry == "" {
		return protocol.Model{}, "", fmt.Errorf("%w", errNoRegistry)
	}

	s3PathIn := strings.TrimPrefix(dataSource, "s3://")
	s3PathOut := strings.TrimPrefix(outputURI, "s3://")
	if !strings.HasSuffix(s3PathOut, "/") {
		s3PathOut += "/"
	}
	s3PathOut += fingerprintHex

	envVars := buildS3ContainerEnv(cfg, s3PathIn, s3PathOut)

	entrypoint, _, err := client.ImageEntrypoint(ctx, modelImage)
	if err != nil {
		return protocol.Model{}, "", fmt.Errorf("inspect image entrypoint: %w", err)
	}

	wrappedImage, err := WrapImage(ctx, client, modelImage, fingerprintHex, cfg.Registry, cfg.RegistryAuth.Reveal(), entrypoint)
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

type buildScheduleJobParams struct {
	Fingerprint  string
	ModelImage   string
	DataSource   string
	OutputURI    string
	ScheduleCron string
	Wrapped      bool
	Model        protocol.Model
	Resources    protocol.Resources
	Labels       map[string]string
}

// buildScheduleJob assembles a protocol.Job from its parts.
func buildScheduleJob(p buildScheduleJobParams) *protocol.Job {
	name := fmt.Sprintf("coach-container-runner-%s", sanitizeImageName(p.ModelImage))
	job := &protocol.Job{
		Fingerprint: p.Fingerprint,
		Name:        name,
		IsRecurring: p.ScheduleCron != "",
		IsWrapped:   p.Wrapped,
		Model:       p.Model,
		Data: protocol.Data{
			Sources:   []string{p.DataSource},
			MountPath: "/data",
		},
		Output: protocol.Output{
			Destination: p.OutputURI,
			MountPath:   "/output",
		},
		Resources: p.Resources,
		Labels:    p.Labels,
	}

	if p.ScheduleCron != "" {
		job.Schedule = &protocol.Schedule{Cron: p.ScheduleCron, Timezone: "UTC"}
	}

	return job
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
	result, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration())
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
	if _, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration()); err != nil {
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
	result, err := InvokeDriverWithContext(ctx, backend.Driver, spec, backend.Config, cfg.DriverTimeoutDuration())
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
