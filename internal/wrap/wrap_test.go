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

func TestModuleRootDir(t *testing.T) {
	t.Run("found", func(t *testing.T) {
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

		expected, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		actual, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		assert.Equal(t, expected, actual)
	})

	t.Run("not found", func(t *testing.T) {
		dir := t.TempDir()

		cur, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() { os.Chdir(cur) })

		_, err = moduleRootDir()
		assert.ErrorIs(t, err, errGoModNotFound)
	})
}

func TestCopy(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "single file",
			run: func(t *testing.T) {
				tmp := t.TempDir()
				src := filepath.Join(tmp, "src.txt")
				dst := filepath.Join(tmp, "sub", "dst.txt")

				content := []byte("hello world\nline two\n")
				require.NoError(t, os.WriteFile(src, content, 0o644))
				require.NoError(t, copyFile(src, dst))

				got, err := os.ReadFile(dst)
				require.NoError(t, err)
				assert.Equal(t, content, got)
			},
		},
		{
			name: "source not found",
			run: func(t *testing.T) {
				err := copyFile("/nonexistent/path", filepath.Join(t.TempDir(), "dst"))
				assert.Error(t, err)
			},
		},
		{
			name: "full directory tree",
			run: func(t *testing.T) {
				tmp := t.TempDir()
				srcRoot := filepath.Join(tmp, "src")
				dstRoot := filepath.Join(tmp, "dst")

				for _, f := range []struct{ path, content string }{
					{filepath.Join(srcRoot, "a.txt"), "a"},
					{filepath.Join(srcRoot, "sub", "b.txt"), "b"},
					{filepath.Join(srcRoot, "sub", "deep", "c.txt"), "c"},
				} {
					require.NoError(t, os.MkdirAll(filepath.Dir(f.path), 0o755))
					require.NoError(t, os.WriteFile(f.path, []byte(f.content), 0o644))
				}

				require.NoError(t, copyDir(srcRoot, dstRoot))

				for _, rel := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt"} {
					src, _ := os.ReadFile(filepath.Join(srcRoot, rel))
					dst, _ := os.ReadFile(filepath.Join(dstRoot, rel))
					assert.Equal(t, src, dst, "file %s", rel)
				}
			},
		},
		{
			name: "empty directory",
			run: func(t *testing.T) {
				tmp := t.TempDir()
				srcRoot := filepath.Join(tmp, "src")
				dstRoot := filepath.Join(tmp, "dst")
				require.NoError(t, os.MkdirAll(srcRoot, 0o755))
				require.NoError(t, copyDir(srcRoot, dstRoot))
				_, err := os.Stat(dstRoot)
				require.NoError(t, err)
			},
		},
		{
			name: "source dir not found",
			run: func(t *testing.T) {
				err := copyDir("/nonexistent", filepath.Join(t.TempDir(), "dst"))
				assert.Error(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
