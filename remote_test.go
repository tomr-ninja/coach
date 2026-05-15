package coach

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/validate"
	"github.com/tomr-ninja/coach/protocol"
)

func TestResolveS3ArtifactFingerprint(t *testing.T) {
	s3in := "s3://bucket/data"
	s3out := "s3://bucket/output"

	tests := []struct {
		name        string
		digestStr   string
		wantErr     error
		wantContain string
	}{
		{
			name:        "invalid hex characters",
			digestStr:   "xyz%%%invalid%%%hex",
			wantContain: "invalid image digest hex",
		},
		{
			name:        "wrong digest length (16 bytes)",
			digestStr:   "aabbccddeeff00112233445566778899",
			wantErr:     errInvalidImageDigest,
			wantContain: "expected 32 bytes, got 16",
		},
		{
			name:        "wrong digest length (33 bytes)",
			digestStr:   "aabbccddeeff00112233445566778899aabbccddeeff0011223344556677889900",
			wantErr:     errInvalidImageDigest,
			wantContain: "expected 32 bytes, got 33",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolveS3ArtifactFingerprint(
				context.Background(),
				tt.digestStr,
				s3in,
				s3out,
				&config.Config{},
				false,
			)
			assert.Error(t, err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			if tt.wantContain != "" {
				assert.Contains(t, err.Error(), tt.wantContain)
			}
		})
	}
}

func TestRemoteRun_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()

	runValidationTests(t, []validationTest{
		{
			name: "invalid model image",
			fn: func(ctx context.Context) error {
				_, _, _, err := RemoteRun(ctx, &config.Config{}, "", "", "s3://in", "s3://out", nil, "", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrModelImageEmpty,
		},
		{
			name: "invalid data source",
			fn: func(ctx context.Context) error {
				_, _, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", "", "s3://out", nil, "", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
		{
			name: "invalid output URI",
			fn: func(ctx context.Context) error {
				_, _, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", "s3://in", "", nil, "", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
		{
			name: "invalid script name",
			fn: func(ctx context.Context) error {
				_, _, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", "s3://in", "s3://out", nil, "../bad", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrScriptNamePathSep,
		},
		{
			name: "no backend configured",
			fn: func(ctx context.Context) error {
				_, _, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", tmpDir, tmpDir, nil, "", protocol.Resources{}, nil, false)
				return err
			},
			wantContain: "no backend configured",
		},
	})
}

func TestRemoteSchedule_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()

	runValidationTests(t, []validationTest{
		{
			name: "invalid model image",
			fn: func(ctx context.Context) error {
				_, err := RemoteSchedule(ctx, &config.Config{}, "", "", "s3://in", "s3://out", "0 2 * * *", nil, "", protocol.Resources{}, nil)
				return err
			},
			wantErr: validate.ErrModelImageEmpty,
		},
		{
			name: "invalid data source",
			fn: func(ctx context.Context) error {
				_, err := RemoteSchedule(ctx, &config.Config{}, "", "ubuntu:22.04", "", "s3://out", "0 2 * * *", nil, "", protocol.Resources{}, nil)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
		{
			name: "invalid output URI",
			fn: func(ctx context.Context) error {
				_, err := RemoteSchedule(ctx, &config.Config{}, "", "ubuntu:22.04", "s3://in", "", "0 2 * * *", nil, "", protocol.Resources{}, nil)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
		{
			name: "invalid cron expression",
			fn: func(ctx context.Context) error {
				_, err := RemoteSchedule(ctx, &config.Config{}, "", "ubuntu:22.04", "s3://in", "s3://out", "not-a-cron", nil, "", protocol.Resources{}, nil)
				return err
			},
			wantErr: validate.ErrCronExpression,
		},
		{
			name: "invalid script name",
			fn: func(ctx context.Context) error {
				_, err := RemoteSchedule(ctx, &config.Config{}, "", "ubuntu:22.04", "s3://in", "s3://out", "0 2 * * *", nil, "..", protocol.Resources{}, nil)
				return err
			},
			wantErr: validate.ErrScriptNameTraversal,
		},
		{
			name: "mixed local and s3",
			fn: func(ctx context.Context) error {
				_, err := RemoteSchedule(ctx, &config.Config{}, "", "ubuntu:22.04", tmpDir, "s3://bucket/out", "0 2 * * *", nil, "", protocol.Resources{}, nil)
				return err
			},
			wantErr: validate.ErrMixedLocalS3,
		},
	})
}
