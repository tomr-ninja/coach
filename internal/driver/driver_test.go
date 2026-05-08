package driver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/protocol"
)

func TestLimitedBuffer(t *testing.T) {
	t.Run("writes under limit", func(t *testing.T) {
		lb := &limitedBuffer{}
		n, err := lb.Write([]byte("hello"))
		require.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "hello", lb.String())
	})

	t.Run("exceeds limit errors", func(t *testing.T) {
		lb := &limitedBuffer{}
		data := make([]byte, maxDriverOutput+1)
		_, err := lb.Write(data)
		require.Error(t, err)
		assert.ErrorIs(t, err, errDriverOutputTooLarge)
	})
}

func TestValidateDriver(t *testing.T) {
	t.Run("valid executable file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "driver")
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
		err := Validate(path)
		require.NoError(t, err)
	})

	t.Run("directory", func(t *testing.T) {
		path := t.TempDir()
		err := Validate(path)
		require.Error(t, err)
		assert.ErrorIs(t, err, errDriverNotFile)
	})

	t.Run("not executable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "driver")
		require.NoError(t, os.WriteFile(path, []byte(""), 0o644))
		err := Validate(path)
		require.Error(t, err)
		assert.ErrorIs(t, err, errDriverNotExecutable)
	})

	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing")
		err := Validate(path)
		require.Error(t, err)
	})
}

func TestInvokeDriverWithContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell scripts")
	}

	t.Run("success", func(t *testing.T) {
		driver := writeFakeDriver(t, `cat > /dev/null
echo '{"success":true,"protocolVersion":1,"submitResult":{"id":"123"}}'`)

		result, err := InvokeWithContext(t.Context(), driver, &protocol.Spec{Type: "submit"}, nil, defaultDriverTimeout)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.Success)
		require.NotNil(t, result.SubmitResult)
		assert.Equal(t, "123", result.SubmitResult.ID)
	})

	t.Run("failure with error", func(t *testing.T) {
		driver := writeFakeDriver(t, `cat > /dev/null
echo '{"success":false,"protocolVersion":1,"error":"bad input"}'`)

		_, err := InvokeWithContext(t.Context(), driver, &protocol.Spec{Type: "submit"}, nil, defaultDriverTimeout)
		require.Error(t, err)
		assert.ErrorIs(t, err, errDriverFailure)
		assert.Contains(t, err.Error(), "bad input")
	})

	t.Run("failure without error", func(t *testing.T) {
		driver := writeFakeDriver(t, `cat > /dev/null
echo '{"success":false,"protocolVersion":1}'`)

		_, err := InvokeWithContext(t.Context(), driver, &protocol.Spec{Type: "submit"}, nil, defaultDriverTimeout)
		require.Error(t, err)
		assert.ErrorIs(t, err, errDriverEmptyError)
	})

	t.Run("version mismatch", func(t *testing.T) {
		driver := writeFakeDriver(t, `cat > /dev/null
echo '{"success":true,"protocolVersion":99}'`)

		_, err := InvokeWithContext(t.Context(), driver, &protocol.Spec{Type: "submit"}, nil, defaultDriverTimeout)
		require.Error(t, err)
		assert.ErrorIs(t, err, errDriverVersion)
	})

	t.Run("invalid json", func(t *testing.T) {
		driver := writeFakeDriver(t, `cat > /dev/null
echo 'not-json'`)

		_, err := InvokeWithContext(t.Context(), driver, &protocol.Spec{Type: "submit"}, nil, defaultDriverTimeout)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse driver output")
	})

	t.Run("non-zero exit", func(t *testing.T) {
		driver := writeFakeDriver(t, `cat > /dev/null
exit 1`)

		_, err := InvokeWithContext(t.Context(), driver, &protocol.Spec{Type: "submit"}, nil, defaultDriverTimeout)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "driver")
		assert.Contains(t, err.Error(), "crashed")
	})

	t.Run("receives backend config", func(t *testing.T) {
		driver := writeFakeDriver(t, `#/bin/sh
config="$COACH_BACKEND_CONFIG"
echo "{\"success\":true,\"protocolVersion\":1,\"submitResult\":{\"id\":\"$config\"}}"`)

		result, err := InvokeWithContext(t.Context(), driver, &protocol.Spec{Type: "submit"}, []byte(`my-backend-config`), defaultDriverTimeout)
		require.NoError(t, err)
		require.NotNil(t, result.SubmitResult)
		assert.Equal(t, "my-backend-config", result.SubmitResult.ID)
	})
}

func writeFakeDriver(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "driver.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	return path
}
