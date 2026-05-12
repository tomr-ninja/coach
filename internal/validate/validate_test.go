package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/protocol"
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

func TestSubmitResult(t *testing.T) {
	t.Run("valid result", func(t *testing.T) {
		err := SubmitResult(&protocol.SubmitResult{ID: "job-123"})
		require.NoError(t, err)
	})

	t.Run("empty ID", func(t *testing.T) {
		err := SubmitResult(&protocol.SubmitResult{ID: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty ID")
	})

	t.Run("nil result handled by caller", func(t *testing.T) {
		// Caller checks for nil before calling SubmitResult.
		// But if they don't, the function will panic (as expected for a programmer error).
		assert.Panics(t, func() {
			SubmitResult(nil)
		})
	})
}

func TestStatusResult(t *testing.T) {
	t.Run("valid result", func(t *testing.T) {
		err := StatusResult(&protocol.StatusResult{ID: "job-123", State: "running"})
		require.NoError(t, err)
	})

	t.Run("empty ID", func(t *testing.T) {
		err := StatusResult(&protocol.StatusResult{ID: "", State: "running"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty ID")
	})

	t.Run("empty State", func(t *testing.T) {
		err := StatusResult(&protocol.StatusResult{ID: "job-123", State: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty State")
	})

	t.Run("both empty", func(t *testing.T) {
		err := StatusResult(&protocol.StatusResult{ID: "", State: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty ID")
	})
}

func TestParseCPU(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  uint32
	}{
		{"empty", "", 0},
		{"millicores exact", "500m", 500},
		{"millicores large", "2000m", 2000},
		{"whole number", "2", 2000},
		{"float", "1.5", 1500},
		{"float with fraction", "0.25", 250},
		{"invalid", "abc", 0},
		{"invalid suffix", "100x", 0},
		{"negative millicores", "-100m", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseCPU(tt.input), "input: %q", tt.input)
		})
	}
}

func TestParseMemory(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  uint32
	}{
		{"empty", "", 0},
		{"bytes bare number", "512", 512},
		{"bytes rounds up to 1", "0", 1},
		{"bytes fraction rounds up", "0.5", 1},
		{"Ki", "1024Ki", 1},
		{"Ki fractional", "512Ki", 1},
		{"Mi exact", "256Mi", 256},
		{"Gi exact", "4Gi", 4096},
		{"Gi fractional", "1.5Gi", 1536},
		{"Ti exact", "2Ti", 2097152},
		{"Pi exact", "1Pi", 1073741824},
		{"K shorthand", "1024K", 1},
		{"K shorthand fractional", "512K", 1},
		{"M shorthand", "512M", 512},
		{"G shorthand", "2G", 2048},
		{"T shorthand", "1T", 1048576},
		{"P shorthand", "1P", 1073741824},
		{"invalid", "abc", 0},
		{"invalid suffix", "100X", 0},
		{"negative bare rounds up", "-100", 1},
		{"Mi negative rounds up", "-128Mi", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseMemory(tt.input), "input: %q", tt.input)
		})
	}
}

func TestParseGPU(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  uint32
	}{
		{"empty", "", 0},
		{"zero", "0", 0},
		{"one", "1", 1},
		{"eight", "8", 8},
		{"invalid", "abc", 0},
		{"float invalid", "1.5", 0},
		{"negative", "-1", 0},
		{"large", "2147483647", 2147483647},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ParseGPU(tt.input), "input: %q", tt.input)
		})
	}
}

func TestParseResources(t *testing.T) {
	got := ParseResources("500m", "256Mi", "2", "H100")
	want := protocol.Resources{
		CPUMillicores: 500,
		MemoryMi:      256,
		GPU:           2,
		GPUType:       "H100",
	}
	assert.Equal(t, want, got)

	got2 := ParseResources("", "", "", "")
	want2 := protocol.Resources{}
	assert.Equal(t, want2, got2)
}
