package coach

import (
	"crypto/sha256"
	"os"
	"path/filepath"
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
		{"not s3", "http://bucket/key", "", "", true, errNotS3URI},
		{"local path", "./data", "", "", true, errNotS3URI},
		{"empty", "", "", "", true, errNotS3URI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := parseS3URI(tt.input)
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

func TestCollectDataChecksums(t *testing.T) {
	t.Run("single file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello"), 0o644))

		got, err := CollectDataChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
		want := sha256.Sum256([]byte("hello"))
		assert.Equal(t, want, got[0])
	})

	t.Run("multiple files sorted order", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))

		got, err := CollectDataChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 2)
	})

	t.Run("ignores directories", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))

		got, err := CollectDataChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
	})

	t.Run("coachignore", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("skip"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".coachignore"), []byte("skip.txt\n"), 0o644))

		got, err := CollectDataChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
		want := sha256.Sum256([]byte("keep"))
		assert.Equal(t, want, got[0])
	})

	t.Run("coachinclude whitelist", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".coachinclude"), []byte("a.txt\n"), 0o644))

		got, err := CollectDataChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
		want := sha256.Sum256([]byte("a"))
		assert.Equal(t, want, got[0])
	})

	t.Run("ignores meta files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("x"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".coachignore"), []byte("\n"), 0o644))

		got, err := CollectDataChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
	})

	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		got, err := CollectDataChecksums(dir)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
