package artifact

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectChecksums(t *testing.T) {
	t.Run("single file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello"), 0o644))

		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
		want := sha256.Sum256([]byte("hello"))
		assert.Equal(t, want, got[0])
	})

	t.Run("multiple files sorted order", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))

		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 2)
	})

	t.Run("ignores directories", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))

		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
	})

	t.Run("coachignore", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("skip"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".coachignore"), []byte("skip.txt\n"), 0o644))

		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
		want := sha256.Sum256([]byte("keep"))
		assert.Equal(t, want, got[0])
	})

	t.Run("ignores meta files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("x"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".coachignore"), []byte("\n"), 0o644))

		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
	})

	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("subdirectory files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o644))

		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("non-existent path", func(t *testing.T) {
		_, err := CollectChecksums(filepath.Join(t.TempDir(), "does-not-exist"))
		require.Error(t, err)
	})
}

func TestFingerprint(t *testing.T) {
	digest := sha256.Sum256([]byte("model"))
	chk1 := sha256.Sum256([]byte("a"))
	chk2 := sha256.Sum256([]byte("b"))

	// Order should not matter
	fp1 := Fingerprint(digest, [][32]byte{chk1, chk2})
	fp2 := Fingerprint(digest, [][32]byte{chk2, chk1})
	assert.Equal(t, fp1, fp2, "fingerprint should be order-independent")

	// Different inputs -> different output
	fp3 := Fingerprint(digest, [][32]byte{chk1})
	assert.NotEqual(t, fp1, fp3, "different checksums should yield different fingerprint")

	// Empty checksums still works
	fp4 := Fingerprint(digest, nil)
	assert.NotEqual(t, Zero, fp4)
}

func TestPrepareDir(t *testing.T) {
	t.Run("creates dir", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "artifacts")
		err := PrepareDir(path, false)
		require.NoError(t, err)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("errors when exists and not forced", func(t *testing.T) {
		path := t.TempDir()
		err := PrepareDir(path, false)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrExists)
	})

	t.Run("removes and recreates when forced", func(t *testing.T) {
		path := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(path, "old.txt"), []byte("x"), 0o644))
		err := PrepareDir(path, true)
		require.NoError(t, err)
		entries, err := os.ReadDir(path)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
}

func TestValidateDir(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		path := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(path, "out.txt"), []byte("x"), 0o644))
		err := ValidateDir(path)
		require.NoError(t, err)
	})

	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing")
		err := ValidateDir(path)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissing)
	})

	t.Run("empty", func(t *testing.T) {
		path := t.TempDir()
		err := ValidateDir(path)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmpty)
	})
}
