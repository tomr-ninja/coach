package schedule

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/protocol"
)

func TestBuildJob(t *testing.T) {
	tests := []struct {
		name  string
		p     BuildJobParams
		check func(t *testing.T, job *protocol.Job)
	}{
		{
			name: "one-off with command and labels",
			p: BuildJobParams{
				ImageDigest:  "abc123",
				ModelImage:   "registry.io/my/model:v1",
				DataSource:   "/data",
				OutputURI:    "/output",
				ScheduleCron: "",
				Wrapped:      false,
				Model: protocol.Model{
					Image:   "registry.io/my/model:v1",
					Command: []string{"python", "train.py"},
				},
				Resources: protocol.Resources{
					CPUMillicores: 500,
					MemoryMi:      256,
				},
				Labels: map[string]string{"env": "prod"},
			},
			check: func(t *testing.T, job *protocol.Job) {
				require.NotNil(t, job)
				assert.Equal(t, "abc123", job.ImageDigest)
				assert.Equal(t, "coach-container-runner-model-v1", job.Name)
				assert.False(t, job.IsRecurring)
				assert.False(t, job.IsWrapped)
				assert.Nil(t, job.Schedule)
				assert.Equal(t, "registry.io/my/model:v1", job.Model.Image)
				assert.Equal(t, []string{"python", "train.py"}, job.Model.Command)
				assert.Equal(t, []string{"/data"}, job.Data.Sources)
				assert.Equal(t, "/data", job.Data.MountPath)
				assert.Equal(t, "/output", job.Output.Destination)
				assert.Equal(t, "/output", job.Output.MountPath)
				assert.Equal(t, uint32(500), job.Resources.CPUMillicores)
				assert.Equal(t, uint32(256), job.Resources.MemoryMi)
				assert.Equal(t, map[string]string{"env": "prod"}, job.Labels)
			},
		},
		{
			name: "recurring wrapped with script and gpu",
			p: BuildJobParams{
				ImageDigest:  "def456",
				ModelImage:   "ubuntu:22.04",
				DataSource:   "s3://bucket/data",
				OutputURI:    "s3://bucket/output",
				ScheduleCron: "0 2 * * *",
				Wrapped:      true,
				Model: protocol.Model{
					Image:   "coach-wrapped-ubuntu-22.04:def456789012",
					Script:  "train.sh",
					EnvVars: map[string]string{"KEY": "val"},
				},
				Resources: protocol.Resources{
					GPU:     1,
					GPUType: "H100",
				},
			},
			check: func(t *testing.T, job *protocol.Job) {
				require.NotNil(t, job)
				assert.Equal(t, "def456", job.ImageDigest)
				assert.Equal(t, "coach-container-runner-ubuntu-22.04", job.Name)
				assert.True(t, job.IsRecurring)
				assert.True(t, job.IsWrapped)
				require.NotNil(t, job.Schedule)
				assert.Equal(t, "0 2 * * *", job.Schedule.Cron)
				assert.Equal(t, "UTC", job.Schedule.Timezone)
				assert.Empty(t, job.Model.Command)
				assert.Equal(t, "train.sh", job.Model.Script)
				assert.Equal(t, map[string]string{"KEY": "val"}, job.Model.EnvVars)
				assert.Equal(t, uint32(1), job.Resources.GPU)
				assert.Equal(t, "H100", job.Resources.GPUType)
				assert.Nil(t, job.Labels)
			},
		},
		{
			name: "sanitized image name",
			p: BuildJobParams{
				ImageDigest: "fp",
				ModelImage:  "registry.io/team/my-image:v1.0.0",
				DataSource:  "/data",
				OutputURI:   "/output",
				Model: protocol.Model{
					Image: "registry.io/team/my-image:v1.0.0",
				},
			},
			check: func(t *testing.T, job *protocol.Job) {
				assert.Equal(t, "coach-container-runner-my-image-v1.0.0", job.Name)
			},
		},
		{
			name: "minimal inputs",
			p: BuildJobParams{
				ImageDigest: "minimal",
				ModelImage:  "img:latest",
				DataSource:  "/d",
				OutputURI:   "/o",
				Model: protocol.Model{
					Image: "img:latest",
				},
			},
			check: func(t *testing.T, job *protocol.Job) {
				assert.Equal(t, "minimal", job.ImageDigest)
				assert.Equal(t, "coach-container-runner-img-latest", job.Name)
				assert.False(t, job.IsRecurring)
				assert.Nil(t, job.Schedule)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := BuildJob(tt.p)
			tt.check(t, job)
		})
	}
}
