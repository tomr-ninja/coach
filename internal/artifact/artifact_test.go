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

	t.Run("coachinclude whitelist", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".coachinclude"), []byte("a.txt\n"), 0o644))

		got, err := CollectChecksums(dir)
		require.NoError(t, err)
		require.Len(t, got, 1)
		want := sha256.Sum256([]byte("a"))
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
}
