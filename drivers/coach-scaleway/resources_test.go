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
	job := &protocol.Job{
		Name:        "test-job",
		Fingerprint: "abc123",
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
	}

	got, err := buildJobDefinition(job, "project-42")
	require.NoError(t, err)

	assert.Equal(t, "test-job", got.Name)
	assert.Equal(t, uint32(1000), got.CPULimit)
	assert.Equal(t, uint32(2048), got.MemoryLimit)
	assert.Equal(t, "myimage:latest", got.ImageURI)
	assert.Equal(t, "project-42", got.ProjectID)
	assert.Equal(t, []string{"python", "train.py"}, got.StartupCommand)
	assert.Equal(t, "abc123", got.Description)
	assert.Equal(t, uint32(1024), got.LocalStorageCapacity)
	assert.Equal(t, map[string]string{"FOO": "bar"}, got.EnvironmentVariables)
	assert.NotNil(t, got.CronSchedule)
	assert.Equal(t, "0 * * * *", got.CronSchedule.Schedule)
	assert.Equal(t, "Europe/Paris", got.CronSchedule.Timezone)
}

func TestBuildJobDefinitionDefaults(t *testing.T) {
	job := &protocol.Job{
		Name: "minimal",
		Model: protocol.Model{
			Image:  "img",
			Script: "run.sh",
		},
	}

	got, err := buildJobDefinition(job, "proj")
	require.NoError(t, err)

	assert.Equal(t, uint32(defaultCPU), got.CPULimit)
	assert.Equal(t, uint32(defaultMem), got.MemoryLimit)
	assert.Equal(t, []string{"/bin/sh", "-c", "run.sh"}, got.StartupCommand)
	assert.Nil(t, got.CronSchedule)
}

func TestBuildJobDefinitionScriptOverridesEmptyCommand(t *testing.T) {
	job := &protocol.Job{
		Name: "script-test",
		Model: protocol.Model{
			Image:   "img",
			Command: []string{},
			Script:  "echo hello",
		},
	}

	got, err := buildJobDefinition(job, "proj")
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/sh", "-c", "echo hello"}, got.StartupCommand)
}

func TestBuildJobDefinitionCommandOverridesScript(t *testing.T) {
	job := &protocol.Job{
		Name: "cmd-test",
		Model: protocol.Model{
			Image:   "img",
			Command: []string{"/app/run"},
			Script:  "echo hello",
		},
	}

	got, err := buildJobDefinition(job, "proj")
	require.NoError(t, err)
	assert.Equal(t, []string{"/app/run"}, got.StartupCommand)
}

func TestBuildJobDefinitionCronDefaultsTimezone(t *testing.T) {
	job := &protocol.Job{
		Name: "cron-test",
		Model: protocol.Model{
			Image: "img",
		},
		Schedule: &protocol.Schedule{
			Cron: "0 0 * * *",
		},
	}

	got, err := buildJobDefinition(job, "proj")
	require.NoError(t, err)
	assert.NotNil(t, got.CronSchedule)
	assert.Equal(t, "UTC", got.CronSchedule.Timezone)
}

func TestBuildJobDefinitionEmptyName(t *testing.T) {
	job := &protocol.Job{
		Name: "",
		Model: protocol.Model{
			Image: "img",
		},
	}

	_, err := buildJobDefinition(job, "proj")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job.Name is empty")
}
