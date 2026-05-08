package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
