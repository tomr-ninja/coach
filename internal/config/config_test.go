package config

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
)

func TestExpandEnvString(t *testing.T) {
	t.Run("not env var", func(t *testing.T) {
		got, err := expandEnvString("hello")
		require.NoError(t, err)
		assert.Equal(t, "hello", got)
	})

	t.Run("env var set", func(t *testing.T) {
		t.Setenv("COACH_TEST_VAR", "value123")
		got, err := expandEnvString("$COACH_TEST_VAR")
		require.NoError(t, err)
		assert.Equal(t, "value123", got)
	})

	t.Run("env var not set", func(t *testing.T) {
		got, err := expandEnvString("$COACH_MISSING_VAR")
		require.Error(t, err)
		assert.ErrorIs(t, err, errEnvVarNotSet)
		assert.Contains(t, coacherrors.GetHint(err), "replace \"COACH_MISSING_VAR\" with a literal value")
		assert.Empty(t, got)
	})
}

func TestExpandEnvInConfig(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := expandEnvInConfig(nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("no env vars", func(t *testing.T) {
		raw := json.RawMessage(`{"key":"value"}`)
		got, err := expandEnvInConfig(raw)
		require.NoError(t, err)
		assert.Equal(t, raw, got)
	})

	t.Run("expands env var", func(t *testing.T) {
		t.Setenv("COACH_SECRET", "my-secret")
		raw := json.RawMessage(`{"key":"$COACH_SECRET"}`)
		got, err := expandEnvInConfig(raw)
		require.NoError(t, err)
		assert.Equal(t, `{"key":"my-secret"}`, string(got))
	})

	t.Run("missing env var errors", func(t *testing.T) {
		raw := json.RawMessage(`{"key":"$COACH_MISSING"}`)
		_, err := expandEnvInConfig(raw)
		require.Error(t, err)
		assert.ErrorIs(t, err, errEnvVarNotSet)
		assert.Contains(t, coacherrors.GetHint(err), "replace \"COACH_MISSING\" with a literal value")
	})

	t.Run("multiple env vars", func(t *testing.T) {
		t.Setenv("COACH_A", "alpha")
		t.Setenv("COACH_B", "beta")
		raw := json.RawMessage(`{"a":"$COACH_A","b":"$COACH_B"}`)
		got, err := expandEnvInConfig(raw)
		require.NoError(t, err)
		assert.Equal(t, `{"a":"alpha","b":"beta"}`, string(got))
	})
}

func TestExpandEnvInStruct(t *testing.T) {
	t.Run("expands fields", func(t *testing.T) {
		t.Setenv("COACH_REG", "my-registry")
		cfg := Config{Registry: "$COACH_REG"}
		err := expandEnvInStruct(&cfg)
		require.NoError(t, err)
		assert.Equal(t, "my-registry", cfg.Registry)
	})

	t.Run("ignores non-pointer", func(t *testing.T) {
		err := expandEnvInStruct(Config{})
		require.NoError(t, err)
	})

	t.Run("ignores nil pointer", func(t *testing.T) {
		var cfg *Config
		err := expandEnvInStruct(cfg)
		require.NoError(t, err)
	})

	t.Run("missing env var errors", func(t *testing.T) {
		cfg := Config{Registry: "$COACH_MISSING"}
		err := expandEnvInStruct(&cfg)
		require.Error(t, err)
		assert.ErrorIs(t, err, errEnvVarNotSet)
		assert.Contains(t, coacherrors.GetHint(err), "replace \"COACH_MISSING\" with a literal value")
	})
}

func TestConfigBackend(t *testing.T) {
	cfg := Config{
		Backends: map[string]Backend{
			"prod": {Driver: "scaleway"},
		},
		DefaultBackend: "prod",
	}

	t.Run("by name", func(t *testing.T) {
		b, err := cfg.Backend("prod")
		require.NoError(t, err)
		assert.Equal(t, "scaleway", b.Driver)
	})

	t.Run("default", func(t *testing.T) {
		b, err := cfg.Backend("")
		require.NoError(t, err)
		assert.Equal(t, "scaleway", b.Driver)
	})

	t.Run("no default", func(t *testing.T) {
		empty := Config{Backends: map[string]Backend{}}
		_, err := empty.Backend("")
		require.Error(t, err)
		assert.ErrorIs(t, err, errNoBackend)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := cfg.Backend("missing")
		require.Error(t, err)
		assert.ErrorIs(t, err, errBackendNotFound)
	})
}

func TestLoadConfigNotFound(t *testing.T) {
	// Ensure no coach.json in cwd or home
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	_, err := LoadConfig()
	require.Error(t, err)
	assert.ErrorIs(t, err, errConfigNotFound)
}
