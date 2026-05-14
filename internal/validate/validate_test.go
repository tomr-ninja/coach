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
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"valid with tag", "ubuntu:22.04", nil},
		{"valid with registry and tag", "registry.io/user/model:v1.0", nil},
		{"valid with digest", "ubuntu@sha256:abc123", nil},
		{"empty string", "", ErrModelImageEmpty},
		{"whitespace only", "   ", ErrModelImageEmpty},
		{"missing tag", "ubuntu", ErrModelImageNoTag},
		{"missing tag with registry", "registry.io/user/model", ErrModelImageNoTag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ModelImage(tt.input)
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
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
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"empty is valid", "", nil},
		{"standard 5-field", "0 2 * * *", nil},
		{"step values", "*/5 * * * *", nil},
		{"invalid", "not-a-cron", ErrCronExpression},
		{"too many fields", "0 0 0 0 0 0", ErrCronExpression},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Cron(tt.input)
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestScriptName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"valid py", "train.py", nil},
		{"valid dash", "run-model", nil},
		{"valid underscore", "script_1.sh", nil},
		{"empty", "", ErrScriptNameEmpty},
		{"contains slash", "../etc/passwd", ErrScriptNamePathSep},
		{"contains backslash", "..\\etc\\passwd", ErrScriptNamePathSep},
		{"contains dot-dot", "foo..bar", ErrScriptNameTraversal},
		{"just dot-dot", "..", ErrScriptNameTraversal},
		{"whitespace only", "   ", ErrScriptNameEmpty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ScriptName(tt.input)
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestLocalVsS3(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		output  string
		wantErr error
	}{
		{"both local", "/data", "/output", nil},
		{"both s3", "s3://bucket/data", "s3://bucket/output", nil},
		{"mixed local-s3", "/data", "s3://bucket/output", ErrMixedLocalS3},
		{"mixed s3-local", "s3://bucket/data", "/output", ErrMixedLocalS3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := LocalVsS3(tt.data, tt.output)
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
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

	t.Run("nil result panics", func(t *testing.T) {
		assert.Panics(t, func() { SubmitResult(nil) })
	})
}

func TestStatusResult(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		state     string
		wantErr   bool
		wantPanic bool
		contain   string
	}{
		{"valid", "job-123", "running", false, false, ""},
		{"empty ID", "", "running", true, false, "empty ID"},
		{"empty State", "job-123", "", true, false, "empty State"},
		{"both empty", "", "", true, false, "empty ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := StatusResult(&protocol.StatusResult{ID: tt.id, State: tt.state})
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.contain)
			} else {
				require.NoError(t, err)
			}
		})
	}
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
