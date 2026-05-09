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

func TestDirPath(t *testing.T) {
	t.Run("valid local directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, DirPath(dir))
	})

	t.Run("missing local directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		err := DirPath(dir)
		require.ErrorIs(t, err, ErrDirMissing)
	})

	t.Run("local file not directory", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
		err := DirPath(f)
		require.ErrorIs(t, err, ErrDirNotDir)
	})

	t.Run("empty string", func(t *testing.T) {
		err := DirPath("")
		require.ErrorIs(t, err, ErrDirEmpty)
	})

	t.Run("valid s3 path", func(t *testing.T) {
		require.NoError(t, DirPath("s3://bucket/prefix"))
	})

	t.Run("s3 path with only bucket", func(t *testing.T) {
		require.NoError(t, DirPath("s3://bucket"))
	})

	t.Run("invalid s3 path", func(t *testing.T) {
		err := DirPath("s3://")
		require.ErrorIs(t, err, ErrDirS3Format)
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

func TestLocalVsS3(t *testing.T) {
	t.Run("both local", func(t *testing.T) {
		require.NoError(t, LocalVsS3("/data", "/output"))
	})

	t.Run("both s3", func(t *testing.T) {
		require.NoError(t, LocalVsS3("s3://bucket/data", "s3://bucket/output"))
	})

	t.Run("mixed local-s3", func(t *testing.T) {
		err := LocalVsS3("/data", "s3://bucket/output")
		require.ErrorIs(t, err, ErrMixedLocalS3)
	})

	t.Run("mixed s3-local", func(t *testing.T) {
		err := LocalVsS3("s3://bucket/data", "/output")
		require.ErrorIs(t, err, ErrMixedLocalS3)
	})
}
