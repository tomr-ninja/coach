package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/internal/config"
)

func TestParseS3URI(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantBucket  string
		wantPrefix  string
		wantErr     bool
		wantErrType error
	}{
		{"simple object", "s3://bucket/key", "bucket", "key", false, nil},
		{"prefix only", "s3://bucket/prefix/", "bucket", "prefix/", false, nil},
		{"bucket only", "s3://bucket", "bucket", "", false, nil},
		{"nested", "s3://bucket/a/b/c", "bucket", "a/b/c", false, nil},
		{"not s3", "http://bucket/key", "", "", true, ErrNotS3URI},
		{"local path", "./data", "", "", true, ErrNotS3URI},
		{"empty", "", "", "", true, ErrNotS3URI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := ParseURI(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrType != nil {
					assert.ErrorIs(t, err, tt.wantErrType)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBucket, bucket)
			assert.Equal(t, tt.wantPrefix, prefix)
		})
	}
}

func TestParseS3PathOut(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantBucket string
		wantPrefix string
		wantErr    bool
	}{
		{"bucket and prefix", "bucket/output/abcd", "bucket", "output/abcd", false},
		{"nested prefix", "my-bucket/data/results/fingerprint", "my-bucket", "data/results/fingerprint", false},
		{"no slash", "bucketonly", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := ParsePathOut(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBucket, bucket)
			assert.Equal(t, tt.wantPrefix, prefix)
		})
	}
}

func TestBuildEnvVars(t *testing.T) {
	t.Run("full config", func(t *testing.T) {
		s3 := config.S3Config{
			AccessKeyID:     config.NewSecureString("my-key"),
			SecretAccessKey: config.NewSecureString("my-secret"),
			Region:          "us-east-1",
			Endpoint:        "https://s3.example.com",
			Provider:        "Minio",
		}
		got := BuildEnvVars(s3)

		assert.Equal(t, "s3", got["RCLONE_CONFIG_S3_TYPE"])
		assert.Equal(t, "Minio", got["RCLONE_CONFIG_S3_PROVIDER"])
		assert.Equal(t, "my-key", got["RCLONE_CONFIG_S3_ACCESS_KEY_ID"])
		assert.Equal(t, "my-secret", got["RCLONE_CONFIG_S3_SECRET_ACCESS_KEY"])
		assert.Equal(t, "us-east-1", got["RCLONE_CONFIG_S3_REGION"])
		assert.Equal(t, "https://s3.example.com", got["RCLONE_CONFIG_S3_ENDPOINT"])
	})

	t.Run("defaults provider to AWS", func(t *testing.T) {
		s3 := config.S3Config{
			AccessKeyID:     config.NewSecureString("k"),
			SecretAccessKey: config.NewSecureString("s"),
			Region:          "r",
		}
		got := BuildEnvVars(s3)
		assert.Equal(t, "AWS", got["RCLONE_CONFIG_S3_PROVIDER"])
	})

	t.Run("empty config produces keys with empty values", func(t *testing.T) {
		got := BuildEnvVars(config.S3Config{})
		assert.Equal(t, "s3", got["RCLONE_CONFIG_S3_TYPE"])
		assert.Equal(t, "AWS", got["RCLONE_CONFIG_S3_PROVIDER"])
		assert.Equal(t, "", got["RCLONE_CONFIG_S3_ACCESS_KEY_ID"])
		assert.Equal(t, "", got["RCLONE_CONFIG_S3_SECRET_ACCESS_KEY"])
		assert.Equal(t, "", got["RCLONE_CONFIG_S3_REGION"])
		assert.Equal(t, "", got["RCLONE_CONFIG_S3_ENDPOINT"])
	})
}
