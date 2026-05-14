package wrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomSuffix(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		s := randomSuffix()
		assert.Len(t, s, 12, "suffix length")
		for _, c := range s {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"invalid hex char %q in suffix %q", c, s)
		}
		assert.False(t, seen[s], "duplicate suffix %q — collision within 100 iterations", s)
		seen[s] = true
	}
}

func TestModuleRootDir_Found(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644))

	subDir := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	cur, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(subDir))
	t.Cleanup(func() { os.Chdir(cur) })

	root, err := moduleRootDir()
	require.NoError(t, err)

	// Resolve symlinks (macOS /var -> /private/var).
	expected, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	actual, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
}

func TestModuleRootDir_NotFound(t *testing.T) {
	dir := t.TempDir()

	cur, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(cur) })

	_, err = moduleRootDir()
	assert.ErrorIs(t, err, errGoModNotFound)
}

func TestCopyFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "sub", "dst.txt")

	content := []byte("hello world\nline two\n")
	require.NoError(t, os.WriteFile(src, content, 0o644))
	require.NoError(t, copyFile(src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestCopyFile_SourceNotFound(t *testing.T) {
	err := copyFile("/nonexistent/path", filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
}

func TestCopyDir(t *testing.T) {
	tmp := t.TempDir()
	srcRoot := filepath.Join(tmp, "src")
	dstRoot := filepath.Join(tmp, "dst")

	writeFile := func(path string, content string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	writeFile(filepath.Join(srcRoot, "a.txt"), "a")
	writeFile(filepath.Join(srcRoot, "sub", "b.txt"), "b")
	writeFile(filepath.Join(srcRoot, "sub", "deep", "c.txt"), "c")

	require.NoError(t, copyDir(srcRoot, dstRoot))

	for _, rel := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt"} {
		src, err := os.ReadFile(filepath.Join(srcRoot, rel))
		require.NoError(t, err)
		dst, err := os.ReadFile(filepath.Join(dstRoot, rel))
		require.NoError(t, err)
		assert.Equal(t, src, dst, "file %s", rel)
	}
}

func TestCopyDir_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	srcRoot := filepath.Join(tmp, "src")
	dstRoot := filepath.Join(tmp, "dst")
	require.NoError(t, os.MkdirAll(srcRoot, 0o755))
	require.NoError(t, copyDir(srcRoot, dstRoot))
	// No files to verify — just assert no error.
	_, err := os.Stat(dstRoot)
	require.NoError(t, err)
}

func TestCopyDir_SourceNotFound(t *testing.T) {
	err := copyDir("/nonexistent", filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
}
