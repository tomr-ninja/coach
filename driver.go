package coach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

var (
	errDriverFailure    = errors.New("driver returned failure")
	errDriverEmptyError = errors.New("driver returned failure without error message")
)

type JobSpec struct {
	Operation      string `json:"operation"`
	ScheduledRunID string `json:"scheduledRunId,omitempty"`
	Job            *Job   `json:"job,omitempty"`
}

type Job struct {
	Fingerprint string            `json:"fingerprint"`
	Name        string            `json:"name,omitempty"`
	FlowName    string            `json:"flowName"`
	Schedule    *Schedule         `json:"schedule,omitempty"`
	Model       Model             `json:"model"`
	Data        Data              `json:"data"`
	Output      Output            `json:"output"`
	Resources   Resources         `json:"resources"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type Schedule struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone,omitempty"`
}

type Model struct {
	Image   string            `json:"image"`
	Command []string          `json:"command,omitempty"`
	Script  string            `json:"script,omitempty"`
	EnvVars map[string]string `json:"envVars,omitempty"`
}

type Data struct {
	Sources   []string `json:"sources"`
	MountPath string   `json:"mountPath"`
}

type Output struct {
	Destination string `json:"destination"`
	MountPath   string `json:"mountPath"`
}

type Resources struct {
	CPU     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	GPU     string `json:"gpu,omitempty"`
	GPUType string `json:"gpuType,omitempty"`
}

type DriverResult struct {
	Success        bool            `json:"success"`
	ScheduledRunID string          `json:"scheduledRunId,omitempty"`
	URL            string          `json:"url,omitempty"`
	Entries        []ScheduleEntry `json:"entries,omitempty"`
	Status         *RunStatus      `json:"status,omitempty"`
	Error          string          `json:"error,omitempty"`
}

type ScheduleEntry struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule,omitempty"`
	Status   string `json:"status"`
	URL      string `json:"url,omitempty"`
}

type RunStatus struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	LastRunAt string `json:"lastRunAt,omitempty"`
	NextRunAt string `json:"nextRunAt,omitempty"`
}

func InvokeDriver(driverPath string, spec *JobSpec, backendConfig json.RawMessage) (*DriverResult, error) {
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal job spec: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), driverPath)
	cmd.Stdin = bytes.NewReader(specJSON)
	cmd.Env = append(os.Environ(), "COACH_BACKEND_CONFIG="+string(backendConfig))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("driver %s failed: %w\nstderr: %s", driverPath, err, stderr.String())
	}

	var result DriverResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parse driver output: %w\nstdout: %s", err, stdout.String())
	}

	if !result.Success {
		if result.Error != "" {
			return nil, fmt.Errorf("%w: %s", errDriverFailure, result.Error)
		}
		return nil, errDriverEmptyError
	}

	return &result, nil
}
