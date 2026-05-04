package coach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tomr-ninja/coach/docker"
	"github.com/tomr-ninja/coach/protocol"
)

var (
	errMixedLocalS3 = errors.New("data source and output must both be local or both be s3")
	errNoRegistry   = errors.New("registry is required for remote S3 runs (set in coach.json)")
)

func ScheduleCreate(backendName, modelImage, dataSource, outputURI, scheduleCron string, command []string, script string, cpu, memory, gpu, gpuType string, labels map[string]string) (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return "", err
	}

	client, err := docker.NewRealDockerClient()
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer client.Close()

	digest, err := client.ImageDigest(modelImage)
	if err != nil {
		return "", fmt.Errorf("image digest: %w", err)
	}

	checksums, err := ResolveChecksums(dataSource, cfg)
	if err != nil {
		return "", fmt.Errorf("resolve checksums: %w", err)
	}

	fingerprint := artifactFingerprint(digest, checksums)
	fingerprintHex := fmt.Sprintf("%x", fingerprint)

	wrap := strings.HasPrefix(dataSource, "s3://")
	if wrap && !strings.HasPrefix(outputURI, "s3://") {
		return "", fmt.Errorf("%w: s3 data source requires s3 output destination", errMixedLocalS3)
	}
	if !wrap && strings.HasPrefix(outputURI, "s3://") {
		return "", fmt.Errorf("%w: local data source requires local output destination", errMixedLocalS3)
	}

	var model protocol.Model

	if wrap {
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

		entrypoint, _, entryErr := client.ImageEntrypoint(modelImage)
		if entryErr != nil {
			return "", fmt.Errorf("inspect image entrypoint: %w", entryErr)
		}

		wrappedImage, wrapErr := WrapImage(context.Background(), client, modelImage, fingerprintHex, cfg.Registry, cfg.RegistryAuth, entrypoint)
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

	flowName := fmt.Sprintf("coach-container-runner-%s", sanitizeImageName(modelImage))
	job := &protocol.Job{
		Fingerprint: fingerprintHex,
		Name:        fmt.Sprintf("train-%s", modelImage),
		FlowName:    flowName,
		Model:       model,
		Data: protocol.Data{
			Sources:   []string{dataSource},
			MountPath: "/data",
		},
		Output: protocol.Output{
			Destination: outputURI,
			MountPath:   "/output",
		},
		Resources: protocol.Resources{
			CPU:     cpu,
			Memory:  memory,
			GPU:     gpu,
			GPUType: gpuType,
		},
		Labels: labels,
	}

	if scheduleCron != "" {
		job.Schedule = &protocol.Schedule{Cron: scheduleCron, Timezone: "UTC"}
	}

	op := "run"
	if scheduleCron != "" {
		op = "schedule"
	}

	spec := &protocol.JobSpec{Operation: op, Job: job}
	result, err := InvokeDriver(backend.Driver, spec, backend.Config)
	if err != nil {
		return "", fmt.Errorf("invoke driver: %w", err)
	}

	return result.ScheduledRunID, nil
}

func ScheduleList(backendName string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return err
	}

	spec := &protocol.JobSpec{Operation: "list"}
	result, err := InvokeDriver(backend.Driver, spec, backend.Config)
	if err != nil {
		return fmt.Errorf("invoke driver: %w", err)
	}

	for _, e := range result.Entries {
		b, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal entry: %w", err)
		}
		fmt.Println(string(b))
	}

	return nil
}

func ScheduleDelete(backendName, runID string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return err
	}

	spec := &protocol.JobSpec{Operation: "delete", ScheduledRunID: runID}
	if _, err := InvokeDriver(backend.Driver, spec, backend.Config); err != nil {
		return fmt.Errorf("invoke driver: %w", err)
	}

	return nil
}

func ScheduleStatus(backendName, runID string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	backend, err := cfg.Backend(backendName)
	if err != nil {
		return err
	}

	spec := &protocol.JobSpec{Operation: "status", ScheduledRunID: runID}
	result, err := InvokeDriver(backend.Driver, spec, backend.Config)
	if err != nil {
		return fmt.Errorf("invoke driver: %w", err)
	}

	if result.Status != nil {
		b, err := json.Marshal(result.Status)
		if err != nil {
			return fmt.Errorf("marshal status: %w", err)
		}
		fmt.Println(string(b))
	}

	return nil
}
