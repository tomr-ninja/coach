package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/protocol"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "hello", "hello"},
		{"with spaces", "hello world", "hello-world"},
		{"mixed unsafe", "foo__bar--baz", "foo-bar-baz"},
		{"trim dashes", "-hello-", "hello"},
		{"too short padded", "ab", "ab-job"},
		{"uppercase lowercased", "HelloWorld", "helloworld"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeName(tt.input), "input: %q", tt.input)
		})
	}
}

func TestSanitizeNamePanicsOnEmpty(t *testing.T) {
	cases := []string{"", string(make([]byte, 60)), "---", "!!!", "..."} // all collapse to empty after sanitizing
	for _, input := range cases {
		input := input // pin loop var
		t.Run(input, func(t *testing.T) {
			require.Panics(t, func() { sanitizeName(input) })
		})
	}
}

func TestDefaultIfZero(t *testing.T) {
	assert.Equal(t, uint32(10), defaultIfZero(0, 10))
	assert.Equal(t, uint32(5), defaultIfZero(5, 10))
}

func TestBuildJobDefinition(t *testing.T) {
	tests := []struct {
		name    string
		job     *protocol.Job
		project string
		check   func(t *testing.T, got *jobDefinitionRequest)
		wantErr string
	}{
		{
			name: "full job with all fields",
			job: &protocol.Job{
				Name:        "test-job",
				ImageDigest: "abc123",
				Model: protocol.Model{
					Image:   "myimage:latest",
					Command: []string{"python", "train.py"},
					EnvVars: map[string]string{"FOO": "bar"},
				},
				Resources: protocol.Resources{
					CPUMillicores: 1000,
					MemoryMi:      2048,
				},
				Schedule: &protocol.Schedule{
					Cron:     "0 * * * *",
					Timezone: "Europe/Paris",
				},
			},
			project: "project-42",
			check: func(t *testing.T, got *jobDefinitionRequest) {
				assert.Equal(t, "test-job", got.Name)
				assert.Equal(t, uint32(1000), got.CPULimit)
				assert.Equal(t, uint32(2048), got.MemoryLimit)
				assert.Equal(t, "myimage:latest", got.ImageURI)
				assert.Equal(t, "project-42", got.ProjectID)
				assert.Equal(t, []string{"python", "train.py"}, got.StartupCommand)
				assert.Equal(t, "abc123", got.Description)
				assert.Equal(t, uint32(1024), got.LocalStorageCapacity)
				assert.Equal(t, map[string]string{"FOO": "bar"}, got.EnvironmentVariables)
				require.NotNil(t, got.CronSchedule)
				assert.Equal(t, "0 * * * *", got.CronSchedule.Schedule)
				assert.Equal(t, "Europe/Paris", got.CronSchedule.Timezone)
			},
		},
		{
			name: "defaults with script",
			job: &protocol.Job{
				Name: "minimal",
				Model: protocol.Model{
					Image:  "img",
					Script: "run.sh",
				},
			},
			project: "proj",
			check: func(t *testing.T, got *jobDefinitionRequest) {
				assert.Equal(t, uint32(defaultCPU), got.CPULimit)
				assert.Equal(t, uint32(defaultMem), got.MemoryLimit)
				assert.Equal(t, []string{"/bin/sh", "-c", "run.sh"}, got.StartupCommand)
				assert.Nil(t, got.CronSchedule)
			},
		},
		{
			name: "script overrides empty command",
			job: &protocol.Job{
				Name: "script-test",
				Model: protocol.Model{
					Image:   "img",
					Command: []string{},
					Script:  "echo hello",
				},
			},
			project: "proj",
			check: func(t *testing.T, got *jobDefinitionRequest) {
				assert.Equal(t, []string{"/bin/sh", "-c", "echo hello"}, got.StartupCommand)
			},
		},
		{
			name: "command overrides script",
			job: &protocol.Job{
				Name: "cmd-test",
				Model: protocol.Model{
					Image:   "img",
					Command: []string{"/app/run"},
					Script:  "echo hello",
				},
			},
			project: "proj",
			check: func(t *testing.T, got *jobDefinitionRequest) {
				assert.Equal(t, []string{"/app/run"}, got.StartupCommand)
			},
		},
		{
			name: "cron defaults timezone",
			job: &protocol.Job{
				Name: "cron-test",
				Model: protocol.Model{
					Image: "img",
				},
				Schedule: &protocol.Schedule{
					Cron: "0 0 * * *",
				},
			},
			project: "proj",
			check: func(t *testing.T, got *jobDefinitionRequest) {
				require.NotNil(t, got.CronSchedule)
				assert.Equal(t, "UTC", got.CronSchedule.Timezone)
			},
		},
		{
			name: "empty name errors",
			job: &protocol.Job{
				Name: "",
				Model: protocol.Model{
					Image: "img",
				},
			},
			project: "proj",
			wantErr: "job.Name is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildJobDefinition(tt.job, tt.project)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			tt.check(t, &got)
		})
	}
}
