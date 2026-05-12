package coach

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/internal/artifact"
	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/driver"
	"github.com/tomr-ninja/coach/internal/schedule"
	"github.com/tomr-ninja/coach/internal/storage"
	"github.com/tomr-ninja/coach/internal/validate"
	"github.com/tomr-ninja/coach/internal/wrap"
	"github.com/tomr-ninja/coach/protocol"
)

var (
	errImageNotLocal      = errors.New("model image must be available locally; pull it first with docker pull")
	errNoSubmitResult     = errors.New("driver returned no submit result")
	errNoListResult       = errors.New("driver returned no list result")
	errNoStatusResult     = errors.New("driver returned no status result")
	errInvalidImageDigest = errors.New("invalid image digest")
)

// RemoteRun submits a one-off training job to a remote backend.
//
// For S3-backed data, the fingerprint is pre-computed from S3 checksums
// and checked against existing artifacts at submit time. If the artifact
// already exists, the command exits early unless force is true.
func RemoteRun(
	ctx context.Context,
	cfg *config.Config,
	backendName, modelImage, dataSource, outputURI string,
	command []string,
	script string,
	resources protocol.Resources,
	labels map[string]string,
	force bool,
) (id string, logS3URI string, err error) {
	if verr := validate.ModelImage(modelImage); verr != nil {
		return "", "", fmt.Errorf("validate model image: %w", verr)
	}
	if verr := validate.DirPath(dataSource); verr != nil {
		return "", "", fmt.Errorf("validate data source: %w", verr)
	}
	if verr := validate.DirPath(outputURI); verr != nil {
		return "", "", fmt.Errorf("validate output destination: %w", verr)
	}
	if script != "" {
		if verr := validate.ScriptName(script); verr != nil {
			return "", "", fmt.Errorf("validate script name: %w", verr)
		}
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return "", "", err
	}

	if err = driver.Validate(backend.Driver); err != nil {
		return "", "", fmt.Errorf("validate driver: %w", err)
	}

	vErr := validate.LocalVsS3(dataSource, outputURI)
	if vErr != nil {
		return "", "", vErr
	}

	client, err := docker.NewClient()
	if err != nil {
		return "", "", fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	imageDigestHex, err := resolveImageDigestHex(ctx, client, modelImage)
	if err != nil {
		return "", "", err
	}

	var model protocol.Model
	var wrappedImage string
	var fpHex string

	if strings.HasPrefix(dataSource, "s3://") {
		model, wrappedImage, fpHex, err = wrapModelForRemoteRun(ctx, client, modelImage, imageDigestHex, dataSource, outputURI, cfg, command, script, backend.Platform, force)
		if err != nil {
			return "", "", err
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
		ScheduleCron: "", // one-off
		Wrapped:      strings.HasPrefix(dataSource, "s3://"),
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
		return "", "", fmt.Errorf("invoke driver: %w", err)
	}

	if result.SubmitResult == nil {
		return "", "", errNoSubmitResult
	}
	if err := validate.SubmitResult(result.SubmitResult); err != nil {
		return "", "", fmt.Errorf("validate submit result: %w", err)
	}

	// Build S3 log path for --watch.
	if fpHex != "" {
		outputPrefix := strings.TrimPrefix(outputURI, "s3://")
		if !strings.HasSuffix(outputPrefix, "/") {
			outputPrefix += "/"
		}
		logS3URI = fmt.Sprintf("s3://%s%s/.coach/log.txt", outputPrefix, fpHex)
	}

	return result.SubmitResult.ID, logS3URI, nil
}

// RemoteSchedule submits a recurring training schedule to a remote backend.
//
// Unlike RemoteRun, the artifact fingerprint is NOT pre-computed. Instead,
// fingerprint computation is deferred to the sidecar at each execution time.
// If a run produces a fingerprint that already exists, the job run fails early
// — no new data means nothing to produce.
func RemoteSchedule(
	ctx context.Context,
	cfg *config.Config,
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

	vErr := validate.LocalVsS3(dataSource, outputURI)
	if vErr != nil {
		return "", vErr
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

	var model protocol.Model
	var wrappedImage string

	if strings.HasPrefix(dataSource, "s3://") {
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
		Wrapped:      strings.HasPrefix(dataSource, "s3://"),
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

// RemoteList returns all jobs/schedules submitted to the backend.
func RemoteList(ctx context.Context, cfg *config.Config, backendName string) ([]protocol.ScheduleEntry, error) {
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

// RemoteDelete removes a job/schedule from the backend.
func RemoteDelete(ctx context.Context, cfg *config.Config, backendName, id string) error {
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

// RemoteStatus returns the current state of a job/schedule.
func RemoteStatus(ctx context.Context, cfg *config.Config, backendName, id string) (*protocol.StatusResult, error) {
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

// resolveS3ArtifactFingerprint computes the artifact fingerprint from S3 data
// checksums and checks for existing artifacts if force is false.
// Returns the raw fingerprint [32]byte and the output prefix (with trailing "/").
func resolveS3ArtifactFingerprint(
	ctx context.Context,
	imageDigestHex, dataSource, outputURI string,
	cfg *config.Config,
	force bool,
) (fingerprint [32]byte, s3PathOutPrefix string, err error) {
	digestBytes, err := hex.DecodeString(imageDigestHex)
	if err != nil {
		return [32]byte{}, "", fmt.Errorf("invalid image digest hex: %w", err)
	}
	if len(digestBytes) != 32 {
		return [32]byte{}, "", fmt.Errorf("%w: expected 32 bytes, got %d", errInvalidImageDigest, len(digestBytes))
	}
	var imageDigest [32]byte
	copy(imageDigest[:], digestBytes)

	dataChecksums, err := storage.Checksums(dataSource, cfg)
	if err != nil {
		return [32]byte{}, "", fmt.Errorf("resolve data checksums: %w", err)
	}

	fingerprint = artifact.Fingerprint(imageDigest, dataChecksums)
	fingerprintHex := fmt.Sprintf("%x", fingerprint)

	s3PathOutPrefix = strings.TrimPrefix(outputURI, "s3://")
	if !strings.HasSuffix(s3PathOutPrefix, "/") {
		s3PathOutPrefix += "/"
	}

	if !force {
		exists, err := storage.ArtifactExists(ctx, cfg, s3PathOutPrefix+fingerprintHex)
		if err != nil {
			return [32]byte{}, "", fmt.Errorf("check artifact exists: %w", err)
		}
		if exists {
			return [32]byte{}, "", fmt.Errorf(
				"%w: s3://%s%s\nHint: use --force to override",
				artifact.ErrExists,
				s3PathOutPrefix, fingerprintHex,
			)
		}
	}

	return fingerprint, s3PathOutPrefix, nil
}

// wrapModelForRemoteRun builds an S3 wrapper image for a one-off remote run.
//
// Unlike wrapModelForS3Schedule, this pre-computes the artifact fingerprint
// from the current S3 data state. If the artifact already exists and force is
// false, an error is returned before any wrapper is built.
func wrapModelForRemoteRun(
	ctx context.Context,
	client *docker.Client,
	modelImage, imageDigestHex, dataSource, outputURI string,
	cfg *config.Config,
	command []string,
	script string,
	targetPlatform string,
	force bool,
) (model protocol.Model, wrapperID string, s3PathOutPrefix string, err error) {
	if cfg.Registry == "" {
		return protocol.Model{}, "", "", wrap.ErrNoRegistry
	}

	fp, s3PathOutPrefix, err := resolveS3ArtifactFingerprint(ctx, imageDigestHex, dataSource, outputURI, cfg, force)
	if err != nil {
		return protocol.Model{}, "", "", err
	}
	fpHex := fmt.Sprintf("%x", fp)

	s3PathIn := strings.TrimPrefix(dataSource, "s3://")
	envVars := s3WrapperEnvVars(cfg, s3PathIn, s3PathOutPrefix, imageDigestHex)

	entrypoint, _, err := client.ImageEntrypoint(ctx, modelImage)
	if err != nil {
		return protocol.Model{}, "", "", fmt.Errorf("inspect image entrypoint: %w", err)
	}

	wrappedImage, err := wrap.Image(ctx, client, modelImage, cfg.Registry, cfg.RegistryAuth.Reveal(), targetPlatform, entrypoint)
	if err != nil {
		return protocol.Model{}, "", "", fmt.Errorf("wrap image: %w", err)
	}

	model = protocol.Model{
		Image:   wrappedImage,
		Command: command,
		Script:  script,
		EnvVars: envVars,
	}

	return model, wrappedImage, fpHex, nil
}

// wrapModelForS3Schedule builds an S3 wrapper image for a scheduled job.
//
// Unlike the one-off RemoteRun path, this does NOT pre-compute the artifact
// fingerprint. Instead it passes imageDigestHex and S3_PATH_OUT_PREFIX as env
// vars. The wrapper entrypoint computes the fingerprint from the actual data at
// execution time and appends it to S3_PATH_OUT_PREFIX.
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

	wrappedImage, err := wrap.Image(ctx, client, modelImage, cfg.Registry, cfg.RegistryAuth.Reveal(), targetPlatform, entrypoint)
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
