package coach

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactFingerprint(t *testing.T) {
	digest := sha256.Sum256([]byte("model"))
	chk1 := sha256.Sum256([]byte("a"))
	chk2 := sha256.Sum256([]byte("b"))

	// Order should not matter
	fp1 := artifactFingerprint(digest, [][32]byte{chk1, chk2})
	fp2 := artifactFingerprint(digest, [][32]byte{chk2, chk1})
	assert.Equal(t, fp1, fp2, "fingerprint should be order-independent")

	// Different inputs -> different output
	fp3 := artifactFingerprint(digest, [][32]byte{chk1})
	assert.NotEqual(t, fp1, fp3, "different checksums should yield different fingerprint")
}

func TestPrepareArtifactDir(t *testing.T) {
	t.Run("creates dir", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "artifacts")
		err := prepareArtifactDir(path, false)
		require.NoError(t, err)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("errors when exists and not forced", func(t *testing.T) {
		path := t.TempDir()
		err := prepareArtifactDir(path, false)
		require.Error(t, err)
		assert.ErrorIs(t, err, errArtifactExists)
	})

	t.Run("removes and recreates when forced", func(t *testing.T) {
		path := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(path, "old.txt"), []byte("x"), 0o644))
		err := prepareArtifactDir(path, true)
		require.NoError(t, err)
		entries, err := os.ReadDir(path)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestValidateArtifactDir(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		path := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(path, "out.txt"), []byte("x"), 0o644))
		err := validateArtifactDir(path)
		require.NoError(t, err)
	})

	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing")
		err := validateArtifactDir(path)
		require.Error(t, err)
		assert.ErrorIs(t, err, errArtifactMissing)
	})

	t.Run("empty", func(t *testing.T) {
		path := t.TempDir()
		err := validateArtifactDir(path)
		require.Error(t, err)
		assert.ErrorIs(t, err, errArtifactEmpty)
	})
}

func TestFileSHA256(t *testing.T) {
	t.Run("matches content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

		got, err := fileSHA256(path)
		require.NoError(t, err)
		want := sha256.Sum256([]byte("hello"))
		assert.Equal(t, want, got)
	})

	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing")
		_, err := fileSHA256(path)
		require.Error(t, err)
	})
}

func TestBuildS3ContainerEnv(t *testing.T) {
	cfg := &Config{
		S3: S3Config{
			AccessKeyID:     "key",
			SecretAccessKey: "secret",
			Region:          "us-west-2",
			Endpoint:        "http://minio:9000",
		},
	}

	got := buildS3ContainerEnv(cfg, "bucket/data", "bucket/output/abcd1234")

	assert.Equal(t, "bucket/data", got["S3_PATH_IN"])
	assert.Equal(t, "bucket/output/abcd1234", got["S3_PATH_OUT"])
	assert.Equal(t, "s3", got["RCLONE_CONFIG_S3_TYPE"])
	assert.Equal(t, "key", got["RCLONE_CONFIG_S3_ACCESS_KEY_ID"])
	assert.Equal(t, "secret", got["RCLONE_CONFIG_S3_SECRET_ACCESS_KEY"])
	assert.Equal(t, "us-west-2", got["RCLONE_CONFIG_S3_REGION"])
	assert.Equal(t, "http://minio:9000", got["RCLONE_CONFIG_S3_ENDPOINT"])
}
