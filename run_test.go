package coach

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/validate"
)

func TestS3WrapperEnvVars(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		modelImage string
		pathIn     string
		pathOut    string
		digest     string
		check      func(t *testing.T, got map[string]string)
	}{
		{
			name:       "full config with endpoint and webhook",
			modelImage: "my-model:v1",
			cfg: &config.Config{
				S3: config.S3Config{
					AccessKeyID:     config.NewSecureString("key"),
					SecretAccessKey: config.NewSecureString("secret"),
					Region:          "us-west-2",
					Endpoint:        "http://minio:9000",
				},
				WebhookURL: "https://hooks.example.com/notify",
			},
			pathIn:  "bucket/data",
			pathOut: "bucket/output/",
			digest:  "abc123",
			check: func(t *testing.T, got map[string]string) {
				assert.Equal(t, "my-model:v1", got["COACH_MODEL_IMAGE"])
				assert.Equal(t, "bucket/output/", got["S3_PATH_OUT_PREFIX"])
				assert.Equal(t, "abc123", got["COACH_IMAGE_DIGEST"])
				assert.Equal(t, "https://hooks.example.com/notify", got["COACH_WEBHOOK_URL"])
				assert.Equal(t, "key", got["AWS_ACCESS_KEY_ID"])
				assert.Equal(t, "secret", got["AWS_SECRET_ACCESS_KEY"])
				assert.Equal(t, "us-west-2", got["AWS_REGION"])
				assert.Equal(t, "http://minio:9000", got["AWS_ENDPOINT_URL"])
			},
		},
		{
			name:       "no endpoint, no webhook",
			modelImage: "other:latest",
			cfg: &config.Config{
				S3: config.S3Config{
					AccessKeyID:     config.NewSecureString("ak"),
					SecretAccessKey: config.NewSecureString("sk"),
					Region:          "eu-west-1",
				},
			},
			pathIn:  "in/data",
			pathOut: "out/",
			digest:  "ff",
			check: func(t *testing.T, got map[string]string) {
				assert.Equal(t, "other:latest", got["COACH_MODEL_IMAGE"])
				assert.Equal(t, "out/", got["S3_PATH_OUT_PREFIX"])
				assert.Equal(t, "ff", got["COACH_IMAGE_DIGEST"])
				assert.Equal(t, "", got["COACH_WEBHOOK_URL"])
				assert.Equal(t, "ak", got["AWS_ACCESS_KEY_ID"])
				assert.Equal(t, "sk", got["AWS_SECRET_ACCESS_KEY"])
				assert.Equal(t, "eu-west-1", got["AWS_REGION"])
				_, hasEndpoint := got["AWS_ENDPOINT_URL"]
				assert.False(t, hasEndpoint, "AWS_ENDPOINT_URL should not be set when endpoint is empty")
			},
		},
		{
			name:       "empty image digest",
			modelImage: "",
			cfg: &config.Config{
				S3: config.S3Config{
					AccessKeyID:     config.NewSecureString("key"),
					SecretAccessKey: config.NewSecureString("secret"),
					Region:          "us-east-1",
				},
			},
			pathIn:  "data",
			pathOut: "out/",
			digest:  "",
			check: func(t *testing.T, got map[string]string) {
				assert.Equal(t, "", got["COACH_MODEL_IMAGE"])
				assert.Equal(t, "", got["COACH_IMAGE_DIGEST"])
				assert.Equal(t, "data", got["S3_PATH_IN"])
				assert.Equal(t, "out/", got["S3_PATH_OUT_PREFIX"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s3WrapperEnvVars(tt.cfg, tt.modelImage, tt.pathIn, tt.pathOut, tt.digest)
			tt.check(t, got)
		})
	}
}

func TestRun_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()

	runValidationTests(t, []validationTest{
		{
			name: "invalid model image",
			fn: func(ctx context.Context) error {
				_, err := Run(ctx, &config.Config{}, "", "/data", "/output", false)
				return err
			},
			wantErr: validate.ErrModelImageEmpty,
		},
		{
			name: "missing data dir",
			fn: func(ctx context.Context) error {
				_, err := Run(ctx, &config.Config{}, "ubuntu:22.04", "/nonexistent", "/output", false)
				return err
			},
			wantErr: validate.ErrDirMissing,
		},
		{
			name: "missing output dir",
			fn: func(ctx context.Context) error {
				_, err := Run(ctx, &config.Config{}, "ubuntu:22.04", tmpDir, "/nonexistent", false)
				return err
			},
			wantErr: validate.ErrDirMissing,
		},
		{
			name: "mixed local and s3",
			fn: func(ctx context.Context) error {
				_, err := Run(ctx, &config.Config{}, "ubuntu:22.04", tmpDir, "s3://bucket/output", false)
				return err
			},
			wantErr: validate.ErrMixedLocalS3,
		},
	})
}

func TestRunLocal_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()

	runValidationTests(t, []validationTest{
		{
			name:    "invalid model image",
			fn:      func(ctx context.Context) error { _, err := RunLocal(ctx, "", "/data", "/output", false); return err },
			wantErr: validate.ErrModelImageEmpty,
		},
		{
			name: "empty data dir",
			fn: func(ctx context.Context) error {
				_, err := RunLocal(ctx, "ubuntu:22.04", "", "/output", false)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
		{
			name: "empty output dir",
			fn: func(ctx context.Context) error {
				_, err := RunLocal(ctx, "ubuntu:22.04", tmpDir, "", false)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
	})
}

func TestRunS3_ValidationErrors(t *testing.T) {
	runValidationTests(t, []validationTest{
		{
			name: "invalid model image",
			fn: func(ctx context.Context) error {
				_, err := RunS3(ctx, &config.Config{}, "ubuntu", "s3://bucket/in", "s3://bucket/out", false)
				return err
			},
			wantErr: validate.ErrModelImageNoTag,
		},
		{
			name: "invalid s3 format",
			fn: func(ctx context.Context) error {
				_, err := RunS3(ctx, &config.Config{}, "ubuntu:22.04", "s3://", "s3://bucket/out", false)
				return err
			},
			wantErr: validate.ErrDirS3Format,
		},
		{
			name: "invalid output s3 format",
			fn: func(ctx context.Context) error {
				_, err := RunS3(ctx, &config.Config{}, "ubuntu:22.04", "s3://bucket/in", "s3://", false)
				return err
			},
			wantErr: validate.ErrDirS3Format,
		},
	})
}

func TestListScripts_ValidationErrors(t *testing.T) {
	runValidationTests(t, []validationTest{
		{
			name:    "invalid model image",
			fn:      func(ctx context.Context) error { _, err := ListScripts(ctx, ""); return err },
			wantErr: validate.ErrModelImageEmpty,
		},
		{
			name:    "missing tag",
			fn:      func(ctx context.Context) error { _, err := ListScripts(ctx, "ubuntu"); return err },
			wantErr: validate.ErrModelImageNoTag,
		},
	})
}

func TestRunScript_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()

	runValidationTests(t, []validationTest{
		{
			name:    "invalid model image",
			fn:      func(ctx context.Context) error { return RunScript(ctx, "", "train.py", nil, "/data", "/output") },
			wantErr: validate.ErrModelImageEmpty,
		},
		{
			name:    "empty script name",
			fn:      func(ctx context.Context) error { return RunScript(ctx, "ubuntu:22.04", "", nil, "/data", "/output") },
			wantErr: validate.ErrScriptNameEmpty,
		},
		{
			name: "script name with path traversal",
			fn: func(ctx context.Context) error {
				return RunScript(ctx, "ubuntu:22.04", "../etc/passwd", nil, tmpDir, tmpDir)
			},
			wantErr: validate.ErrScriptNamePathSep,
		},
		{
			name: "missing data dir",
			fn: func(ctx context.Context) error {
				return RunScript(ctx, "ubuntu:22.04", "train.py", nil, "/nonexistent", "/output")
			},
			wantErr: validate.ErrDirMissing,
		},
		{
			name: "missing output dir",
			fn: func(ctx context.Context) error {
				return RunScript(ctx, "ubuntu:22.04", "train.py", nil, tmpDir, "/nonexistent")
			},
			wantErr: validate.ErrDirMissing,
		},
	})
}

// validationTest is a table-driven subtest that validates a function returns the expected error.
type validationTest struct {
	name        string
	fn          func(context.Context) error
	wantErr     error  // checked with assert.ErrorIs
	wantContain string // checked with assert.Contains (optional)
}

// runValidationTests executes a slice of validationTest as t.Run subtests.
func runValidationTests(t *testing.T, tests []validationTest) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(t.Context())
			require.Error(t, err)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			if tt.wantContain != "" {
				assert.Contains(t, err.Error(), tt.wantContain)
			}
		})
	}
}
