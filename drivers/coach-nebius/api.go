package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nebius/gosdk"
	"github.com/nebius/gosdk/auth"
	ai "github.com/nebius/gosdk/proto/nebius/ai/v1"
	common "github.com/nebius/gosdk/proto/nebius/common/v1"
	compute "github.com/nebius/gosdk/proto/nebius/compute/v1"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tomr-ninja/coach/protocol"
)

type NebiusAPI struct {
	sdk      *gosdk.SDK
	parentID string
	subnetID string
	platform string
	preset   string
	diskGB   int64
	timeout  string
}

type nebiusConfig struct {
	ProjectID      string `json:"project_id"`
	SubnetID       string `json:"subnet_id"`
	IAMToken       string `json:"iam_token"`
	Platform       string `json:"platform"`
	Preset         string `json:"preset"`
	DiskGB         int64  `json:"disk_size_gb"`
	Timeout        string `json:"timeout"`
	ServiceAccount *struct {
		ID         string `json:"id"`
		KeyID      string `json:"key_id"`
		PrivateKey string `json:"private_key_pem"`
	} `json:"service_account,omitempty"`
}

func NewNebiusAPI(ctx context.Context, cfg *nebiusConfig) (*NebiusAPI, error) {
	opts := []gosdk.Option{}

	if cfg.ServiceAccount != nil && cfg.ServiceAccount.ID != "" {
		reader := auth.NewPrivateKeyParser(
			[]byte(cfg.ServiceAccount.PrivateKey),
			cfg.ServiceAccount.KeyID,
			cfg.ServiceAccount.ID,
		)
		opts = append(opts, gosdk.WithCredentials(gosdk.ServiceAccountReader(reader)))
	} else if cfg.IAMToken != "" {
		opts = append(opts, gosdk.WithCredentials(gosdk.IAMToken(cfg.IAMToken)))
	} else {
		return nil, fmt.Errorf("either iam_token or service_account must be provided")
	}

	sdk, err := gosdk.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("initialize nebius sdk: %w", err)
	}

	if cfg.Platform == "" {
		cfg.Platform = defaultPlatform
	}
	if cfg.Preset == "" {
		cfg.Preset = defaultPreset
	}
	if cfg.DiskGB == 0 {
		cfg.DiskGB = defaultDiskGB
	}
	if cfg.Timeout == "" {
		cfg.Timeout = defaultTimeout
	}

	return &NebiusAPI{
		sdk:      sdk,
		parentID: cfg.ProjectID,
		subnetID: cfg.SubnetID,
		platform: cfg.Platform,
		preset:   cfg.Preset,
		diskGB:   cfg.DiskGB,
		timeout:  cfg.Timeout,
	}, nil
}

func (api *NebiusAPI) Close() error {
	return api.sdk.Close()
}

func (api *NebiusAPI) CreateJob(ctx context.Context, job *protocol.Job) (string, error) {
	spec, err := buildJobSpec(job, api)
	if err != nil {
		return "", fmt.Errorf("build job spec: %w", err)
	}

	service := api.sdk.Services().Ai().V1().Job()

	op, err := service.Create(ctx, &ai.CreateJobRequest{
		Metadata: &common.ResourceMetadata{
			ParentId: api.parentID,
			Name:     sanitizeName(job.Name),
		},
		Spec: spec,
	})
	if err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}

	op, err = op.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("wait for job creation: %w", err)
	}

	return op.ResourceID(), nil
}

func (api *NebiusAPI) GetJob(ctx context.Context, id string) (*ai.Job, error) {
	service := api.sdk.Services().Ai().V1().Job()
	return service.Get(ctx, &ai.GetJobRequest{Id: id})
}

func (api *NebiusAPI) ListJobs(ctx context.Context) ([]*ai.Job, error) {
	service := api.sdk.Services().Ai().V1().Job()

	var jobs []*ai.Job
	req := &ai.ListJobsRequest{ParentId: api.parentID}
	for job, err := range service.Filter(ctx, req) {
		if err != nil {
			return nil, fmt.Errorf("list jobs: %w", err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (api *NebiusAPI) CancelJob(ctx context.Context, id string) error {
	service := api.sdk.Services().Ai().V1().Job()
	op, err := service.Cancel(ctx, &ai.CancelJobRequest{Id: id})
	if err != nil {
		return fmt.Errorf("cancel job: %w", err)
	}
	_, err = op.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait for cancel: %w", err)
	}
	return nil
}

func (api *NebiusAPI) DeleteJob(ctx context.Context, id string) error {
	service := api.sdk.Services().Ai().V1().Job()
	op, err := service.Delete(ctx, &ai.DeleteJobRequest{Id: id})
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	_, err = op.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait for delete: %w", err)
	}
	return nil
}

func buildJobSpec(job *protocol.Job, api *NebiusAPI) (*ai.JobSpec, error) {
	model := job.Model

	// Build container command and args
	var containerCommand, args string
	if len(model.Command) > 0 {
		containerCommand = model.Command[0]
		if len(model.Command) > 1 {
			args = strings.Join(model.Command[1:], " ")
		}
	} else if model.Script != "" {
		containerCommand = "bash"
		args = "-c " + model.Script
	}

	// Build environment variables
	var envVars []*ai.JobSpec_EnvironmentVariable
	for k, v := range model.EnvVars {
		envVars = append(envVars, &ai.JobSpec_EnvironmentVariable{
			Name:  k,
			Value: v,
		})
	}

	// Build timeout
	timeout, err := time.ParseDuration(api.timeout)
	if err != nil {
		timeout = 24 * time.Hour
	}

	spec := &ai.JobSpec{
		Image:                model.Image,
		EnvironmentVariables: envVars,
		Platform:             api.platform,
		Preset:               api.preset,
		SubnetId:             api.subnetID,
		ContainerCommand:     containerCommand,
		Args:                 args,
		Disk: &ai.JobSpec_DiskSpec{
			Type:      compute.DiskSpec_NETWORK_SSD,
			SizeBytes: api.diskGB * 1024 * 1024 * 1024,
		},
		Timeout: durationpb.New(timeout),
	}

	return spec, nil
}
