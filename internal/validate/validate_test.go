package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelImage(t *testing.T) {
	t.Run("valid with tag", func(t *testing.T) {
		require.NoError(t, ModelImage("ubuntu:22.04"))
		require.NoError(t, ModelImage("registry.io/user/model:v1.0"))
	})

	t.Run("valid with digest", func(t *testing.T) {
		require.NoError(t, ModelImage("ubuntu@sha256:abc123"))
	})

	t.Run("empty string", func(t *testing.T) {
		err := ModelImage("")
		require.ErrorIs(t, err, ErrModelImageEmpty)
	})

	t.Run("whitespace only", func(t *testing.T) {
		err := ModelImage("   ")
		require.ErrorIs(t, err, ErrModelImageEmpty)
	})

	t.Run("missing tag", func(t *testing.T) {
		err := ModelImage("ubuntu")
		require.ErrorIs(t, err, ErrModelImageNoTag)
	})

	t.Run("missing tag with registry", func(t *testing.T) {
		err := ModelImage("registry.io/user/model")
		require.ErrorIs(t, err, ErrModelImageNoTag)
	})
}

func TestDataPath(t *testing.T) {
	t.Run("valid local directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, DataPath(dir))
	})

	t.Run("missing local directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		err := DataPath(dir)
		require.ErrorIs(t, err, ErrDataPathMissing)
	})

	t.Run("local file not directory", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
		err := DataPath(f)
		require.ErrorIs(t, err, ErrDataPathNotDir)
	})

	t.Run("empty string", func(t *testing.T) {
		err := DataPath("")
		require.ErrorIs(t, err, ErrDataPathEmpty)
	})

	t.Run("valid s3 path", func(t *testing.T) {
		require.NoError(t, DataPath("s3://bucket/prefix"))
	})

	t.Run("s3 path with only bucket", func(t *testing.T) {
		require.NoError(t, DataPath("s3://bucket"))
	})

	t.Run("invalid s3 path", func(t *testing.T) {
		err := DataPath("s3://")
		require.ErrorIs(t, err, ErrDataPathS3Format)
	})
}

func TestOutputDir(t *testing.T) {
	t.Run("valid local directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, OutputDir(dir))
	})

	t.Run("missing local directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		err := OutputDir(dir)
		require.ErrorIs(t, err, ErrOutputDirMissing)
	})

	t.Run("local file not directory", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
		err := OutputDir(f)
		require.ErrorIs(t, err, ErrOutputDirNotDir)
	})

	t.Run("empty string", func(t *testing.T) {
		err := OutputDir("")
		require.ErrorIs(t, err, ErrOutputDirEmpty)
	})

	t.Run("valid s3 path", func(t *testing.T) {
		require.NoError(t, OutputDir("s3://bucket/prefix"))
	})

	t.Run("invalid s3 path", func(t *testing.T) {
		err := OutputDir("s3://")
		require.ErrorIs(t, err, ErrOutputDirS3Format)
	})
}

func TestCron(t *testing.T) {
	t.Run("empty is valid", func(t *testing.T) {
		require.NoError(t, Cron(""))
	})

	t.Run("standard 5-field cron", func(t *testing.T) {
		require.NoError(t, Cron("0 2 * * *"))
		require.NoError(t, Cron("*/5 * * * *"))
	})

	t.Run("invalid cron", func(t *testing.T) {
		err := Cron("not-a-cron")
		require.ErrorIs(t, err, ErrCronExpression)
	})

	t.Run("too many fields", func(t *testing.T) {
		err := Cron("0 0 0 0 0 0")
		require.ErrorIs(t, err, ErrCronExpression)
	})
}

func TestScriptName(t *testing.T) {
	t.Run("valid names", func(t *testing.T) {
		require.NoError(t, ScriptName("train.py"))
		require.NoError(t, ScriptName("run-model"))
		require.NoError(t, ScriptName("script_1.sh"))
	})

	t.Run("empty", func(t *testing.T) {
		err := ScriptName("")
		require.ErrorIs(t, err, ErrScriptNameEmpty)
	})

	t.Run("contains slash", func(t *testing.T) {
		err := ScriptName("../etc/passwd")
		require.ErrorIs(t, err, ErrScriptNamePathSep)
	})

	t.Run("contains backslash", func(t *testing.T) {
		err := ScriptName("..\\etc\\passwd")
		require.ErrorIs(t, err, ErrScriptNamePathSep)
	})

	t.Run("contains dot-dot", func(t *testing.T) {
		err := ScriptName("foo..bar")
		require.ErrorIs(t, err, ErrScriptNameTraversal)
	})

	t.Run("just dot-dot", func(t *testing.T) {
		err := ScriptName("..")
		require.ErrorIs(t, err, ErrScriptNameTraversal)
	})

	t.Run("whitespace only", func(t *testing.T) {
		err := ScriptName("   ")
		require.ErrorIs(t, err, ErrScriptNameEmpty)
	})
}
