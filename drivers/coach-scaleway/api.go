package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"

	"github.com/tomr-ninja/coach/protocol"
)

type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("api error %d: %s", e.StatusCode, e.Body)
}

var errNoJobRuns = errors.New("no job runs found")

type ScalewayAPI struct {
	BaseURL string
	Token   string
	Region  string
	Project string
	Org     string
	client  *http.Client
}

func NewScalewayAPI(baseURL, token, region, project, org string) *ScalewayAPI {
	return &ScalewayAPI{
		BaseURL: baseURL,
		Token:   token,
		Region:  region,
		Project: project,
		Org:     org,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (api *ScalewayAPI) req(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := api.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-Auth-Token", api.Token)
	req.Header.Set("Content-Type", "application/json")
	if api.Org != "" {
		req.Header.Set("X-Auth-Organization-Id", api.Org)
	}

	return api.client.Do(req)
}

func (api *ScalewayAPI) createJobDefinition(jd jobDefinitionRequest) (string, error) {
	resp, err := api.req(context.Background(), "POST", "/job-definitions", jd)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return "", err
	}
	return result.ID, nil
}

func (api *ScalewayAPI) startJobDefinition(id string) error {
	resp, err := api.req(context.Background(), "POST", "/job-definitions/"+id+"/start", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, nil)
}

func (api *ScalewayAPI) getJobDefinition(id string) (*jobDefinition, error) {
	resp, err := api.req(context.Background(), "GET", "/job-definitions/"+id, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var jd jobDefinition
	if err := decodeResponse(resp, &jd); err != nil {
		return nil, err
	}
	return &jd, nil
}

func (api *ScalewayAPI) listJobDefinitions() ([]jobDefinition, error) {
	resp, err := api.req(context.Background(), "GET", "/job-definitions", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		JobDefinitions []jobDefinition `json:"job_definitions"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.JobDefinitions, nil
}

func (api *ScalewayAPI) deleteJobDefinition(id string) error {
	resp, err := api.req(context.Background(), "DELETE", "/job-definitions/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, nil)
}

func (api *ScalewayAPI) latestJobRun(jobDefID string) (*jobRun, error) {
	resp, err := api.req(context.Background(), "GET", "/job-runs?job_definition_id="+jobDefID+"&order_by=created_at_desc&per_page=1", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		JobRuns []jobRun `json:"job_runs"`
	}
	if err := decodeResponse(resp, &result); err != nil {
		return nil, err
	}
	if len(result.JobRuns) == 0 {
		return nil, errNoJobRuns
	}
	return &result.JobRuns[0], nil
}

func decodeResponse(resp *http.Response, v any) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			return fmt.Errorf("parse response: %w\nbody: %s", err, string(body))
		}
	}

	return nil
}

type jobDefinitionRequest struct {
	Name                 string            `json:"name"`
	CPULimit             uint32            `json:"cpu_limit"`
	MemoryLimit          uint32            `json:"memory_limit"`
	ImageURI             string            `json:"image_uri"`
	ProjectID            string            `json:"project_id"`
	LocalStorageCapacity uint32            `json:"local_storage_capacity"`
	StartupCommand       []string          `json:"startup_command,omitempty"`
	EnvironmentVariables map[string]string `json:"environment_variables,omitempty"`
	CronSchedule         *cronScheduleReq  `json:"cron_schedule,omitempty"`
	Description          string            `json:"description,omitempty"`
	JobTimeout           string            `json:"job_timeout,omitempty"`
}

type cronScheduleReq struct {
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone"`
}

type jobDefinition struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	CronSchedule *struct {
		Schedule string `json:"schedule"`
		Timezone string `json:"timezone"`
	} `json:"cron_schedule,omitempty"`
}

type jobRun struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

func buildJobDefinition(job *protocol.Job, projectID string) jobDefinitionRequest {
	model := job.Model

	startupCmd := model.Command
	if len(startupCmd) == 0 && model.Script != "" {
		startupCmd = []string{"/bin/sh", "-c", model.Script}
	}

	var cronReq *cronScheduleReq
	if job.Schedule != nil {
		tz := job.Schedule.Timezone
		if tz == "" {
			tz = "UTC"
		}
		cronReq = &cronScheduleReq{
			Schedule: job.Schedule.Cron,
			Timezone: tz,
		}
	}

	name := job.Name
	if name == "" {
		name = job.FlowName
	}

	env := make(map[string]string)
	maps.Copy(env, model.EnvVars)

	return jobDefinitionRequest{
		Name:                 sanitizeName(name),
		CPULimit:             defaultIfZero(job.Resources.CPUMillicores, defaultCPU),
		MemoryLimit:          defaultIfZero(job.Resources.MemoryMi, defaultMem),
		ImageURI:             model.Image,
		ProjectID:            projectID,
		LocalStorageCapacity: 1024,
		StartupCommand:       startupCmd,
		EnvironmentVariables: env,
		CronSchedule:         cronReq,
		Description:          job.Fingerprint,
	}
}
