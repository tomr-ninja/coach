package coach

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateModelImage(t *testing.T) {
	t.Run("valid with tag", func(t *testing.T) {
		require.NoError(t, ValidateModelImage("ubuntu:22.04"))
		require.NoError(t, ValidateModelImage("registry.io/user/model:v1.0"))
	})

	t.Run("valid with digest", func(t *testing.T) {
		require.NoError(t, ValidateModelImage("ubuntu@sha256:abc123"))
	})

	t.Run("empty string", func(t *testing.T) {
		err := ValidateModelImage("")
		require.ErrorIs(t, err, ErrModelImageEmpty)
	})

	t.Run("whitespace only", func(t *testing.T) {
		err := ValidateModelImage("   ")
		require.ErrorIs(t, err, ErrModelImageEmpty)
	})

	t.Run("missing tag", func(t *testing.T) {
		err := ValidateModelImage("ubuntu")
		require.ErrorIs(t, err, ErrModelImageNoTag)
	})

	t.Run("missing tag with registry", func(t *testing.T) {
		err := ValidateModelImage("registry.io/user/model")
		require.ErrorIs(t, err, ErrModelImageNoTag)
	})
}

func TestValidateDataPath(t *testing.T) {
	t.Run("valid local directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, ValidateDataPath(dir))
	})

	t.Run("missing local directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		err := ValidateDataPath(dir)
		require.ErrorIs(t, err, ErrDataPathMissing)
	})

	t.Run("local file not directory", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
		err := ValidateDataPath(f)
		require.ErrorIs(t, err, ErrDataPathNotDir)
	})

	t.Run("empty string", func(t *testing.T) {
		err := ValidateDataPath("")
		require.ErrorIs(t, err, ErrDataPathEmpty)
	})

	t.Run("valid s3 path", func(t *testing.T) {
		require.NoError(t, ValidateDataPath("s3://bucket/prefix"))
	})

	t.Run("s3 path with only bucket", func(t *testing.T) {
		require.NoError(t, ValidateDataPath("s3://bucket"))
	})

	t.Run("invalid s3 path", func(t *testing.T) {
		err := ValidateDataPath("s3://")
		require.ErrorIs(t, err, ErrDataPathS3Format)
	})
}

func TestValidateOutputDir(t *testing.T) {
	t.Run("valid local directory", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, ValidateOutputDir(dir))
	})

	t.Run("missing local directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		err := ValidateOutputDir(dir)
		require.ErrorIs(t, err, ErrOutputDirMissing)
	})

	t.Run("local file not directory", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.txt")
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
		err := ValidateOutputDir(f)
		require.ErrorIs(t, err, ErrOutputDirNotDir)
	})

	t.Run("empty string", func(t *testing.T) {
		err := ValidateOutputDir("")
		require.ErrorIs(t, err, ErrOutputDirEmpty)
	})

	t.Run("valid s3 path", func(t *testing.T) {
		require.NoError(t, ValidateOutputDir("s3://bucket/prefix"))
	})

	t.Run("invalid s3 path", func(t *testing.T) {
		err := ValidateOutputDir("s3://")
		require.ErrorIs(t, err, ErrOutputDirS3Format)
	})
}

func TestValidateCron(t *testing.T) {
	t.Run("empty is valid", func(t *testing.T) {
		require.NoError(t, ValidateCron(""))
	})

	t.Run("standard 5-field cron", func(t *testing.T) {
		require.NoError(t, ValidateCron("0 2 * * *"))
		require.NoError(t, ValidateCron("*/5 * * * *"))
	})

	t.Run("invalid cron", func(t *testing.T) {
		err := ValidateCron("not-a-cron")
		require.ErrorIs(t, err, ErrCronExpression)
	})

	t.Run("too many fields", func(t *testing.T) {
		err := ValidateCron("0 0 0 0 0 0")
		require.ErrorIs(t, err, ErrCronExpression)
	})
}

func TestValidateScriptName(t *testing.T) {
	t.Run("valid names", func(t *testing.T) {
		require.NoError(t, ValidateScriptName("train.py"))
		require.NoError(t, ValidateScriptName("run-model"))
		require.NoError(t, ValidateScriptName("script_1.sh"))
	})

	t.Run("empty", func(t *testing.T) {
		err := ValidateScriptName("")
		require.ErrorIs(t, err, ErrScriptNameEmpty)
	})

	t.Run("contains slash", func(t *testing.T) {
		err := ValidateScriptName("../etc/passwd")
		require.ErrorIs(t, err, ErrScriptNamePathSep)
	})

	t.Run("contains backslash", func(t *testing.T) {
		err := ValidateScriptName("..\\etc\\passwd")
		require.ErrorIs(t, err, ErrScriptNamePathSep)
	})

	t.Run("contains dot-dot", func(t *testing.T) {
		err := ValidateScriptName("foo..bar")
		require.ErrorIs(t, err, ErrScriptNameTraversal)
	})

	t.Run("just dot-dot", func(t *testing.T) {
		err := ValidateScriptName("..")
		require.ErrorIs(t, err, ErrScriptNameTraversal)
	})

	t.Run("whitespace only", func(t *testing.T) {
		err := ValidateScriptName("   ")
		require.ErrorIs(t, err, ErrScriptNameEmpty)
	})
}
