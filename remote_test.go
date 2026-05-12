package coach

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tomr-ninja/coach/internal/config"
	"github.com/tomr-ninja/coach/internal/validate"
	"github.com/tomr-ninja/coach/protocol"
)

func TestResolveS3ArtifactFingerprint_InvalidHex(t *testing.T) {
	// Invalid hex string (not valid hex characters).
	_, _, err := resolveS3ArtifactFingerprint(
		context.Background(),
		"xyz%%%invalid%%%hex",
		"s3://bucket/data",
		"s3://bucket/output",
		&config.Config{},
		false,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid image digest hex")
}

func TestResolveS3ArtifactFingerprint_WrongDigestLength(t *testing.T) {
	// Valid hex but only 16 bytes instead of 32.
	_, _, err := resolveS3ArtifactFingerprint(
		context.Background(),
		"aabbccddeeff00112233445566778899", // 32 hex chars = 16 bytes
		"s3://bucket/data",
		"s3://bucket/output",
		&config.Config{},
		false,
	)
	assert.Error(t, err)
	assert.ErrorIs(t, err, errInvalidImageDigest)
	assert.Contains(t, err.Error(), "expected 32 bytes, got 16")
}

func TestResolveS3ArtifactFingerprint_WrongDigestLengthTooLong(t *testing.T) {
	// 64 hex chars = 32 bytes, but we're giving 66 chars = 33 bytes.
	_, _, err := resolveS3ArtifactFingerprint(
		context.Background(),
		"aabbccddeeff00112233445566778899aabbccddeeff0011223344556677889900", // 66 hex chars = 33 bytes
		"s3://bucket/data",
		"s3://bucket/output",
		&config.Config{},
		false,
	)
	assert.Error(t, err)
	assert.ErrorIs(t, err, errInvalidImageDigest)
	assert.Contains(t, err.Error(), "expected 32 bytes, got 33")
}

func TestRemoteRun_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()

	runValidationTests(t, []validationTest{
		{
			name: "invalid model image",
			fn: func(ctx context.Context) error {
				_, _, err := RemoteRun(ctx, &config.Config{}, "", "", "s3://in", "s3://out", nil, "", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrModelImageEmpty,
		},
		{
			name: "invalid data source",
			fn: func(ctx context.Context) error {
				_, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", "", "s3://out", nil, "", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
		{
			name: "invalid output URI",
			fn: func(ctx context.Context) error {
				_, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", "s3://in", "", nil, "", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrDirEmpty,
		},
		{
			name: "invalid script name",
			fn: func(ctx context.Context) error {
				_, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", "s3://in", "s3://out", nil, "../bad", protocol.Resources{}, nil, false)
				return err
			},
			wantErr: validate.ErrScriptNamePathSep,
		},
		{
			name: "no backend configured",
			fn: func(ctx context.Context) error {
				_, _, err := RemoteRun(ctx, &config.Config{}, "", "ubuntu:22.04", tmpDir, tmpDir, nil, "", protocol.Resources{}, nil, false)
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
