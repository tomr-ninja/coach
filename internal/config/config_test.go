package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
)

func TestExpandEnvString(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		input   string
		want    string
		wantErr error
	}{
		{"not env var", "", "", "hello", "hello", nil},
		{"env var set", "COACH_TEST_VAR", "value123", "$COACH_TEST_VAR", "value123", nil},
		{"env var not set", "", "", "$COACH_MISSING_VAR", "", errEnvVarNotSet},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}
			got, err := expandEnvString(tt.input)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Contains(t, coacherrors.GetHint(err), "replace")
				assert.Empty(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestExpandEnvInConfig(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		input    string
		want     string
		wantErr  error
		wantHint string
	}{
		{"empty raw", nil, "", "", nil, ""},
		{"no env vars", nil, `{"key":"value"}`, `{"key":"value"}`, nil, ""},
		{"single expansion", map[string]string{"COACH_SECRET": "my-secret"}, `{"key":"$COACH_SECRET"}`, `{"key":"my-secret"}`, nil, ""},
		{"missing env var", nil, `{"key":"$COACH_MISSING"}`, "", errEnvVarNotSet, "COACH_MISSING"},
		{"multiple expansions", map[string]string{"COACH_A": "alpha", "COACH_B": "beta"}, `{"a":"$COACH_A","b":"$COACH_B"}`, `{"a":"alpha","b":"beta"}`, nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var raw json.RawMessage
			if tt.input != "" {
				raw = json.RawMessage(tt.input)
			}
			got, err := expandEnvInConfig(raw)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Contains(t, coacherrors.GetHint(err), tt.wantHint)
			} else {
				require.NoError(t, err)
				if tt.input == "" {
					assert.Empty(t, got)
				} else {
					assert.Equal(t, tt.want, string(got))
				}
			}
		})
	}
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

func TestDriverTimeoutDuration(t *testing.T) {
	t.Run("default when empty", func(t *testing.T) {
		cfg := Config{}
		assert.Equal(t, defaultDriverTimeout, cfg.DriverTimeoutDuration())
	})

	t.Run("parses duration", func(t *testing.T) {
		cfg := Config{DriverTimeout: "10m"}
		assert.Equal(t, 10*time.Minute, cfg.DriverTimeoutDuration())
	})

	t.Run("falls back on invalid", func(t *testing.T) {
		cfg := Config{DriverTimeout: "not-a-duration"}
		assert.Equal(t, defaultDriverTimeout, cfg.DriverTimeoutDuration())
	})
}

// TestExpandEnv_NilBackendsMap verifies that expanding env vars on a Config
// with a nil Backends map doesn't panic (exercised through the public
// expandEnvInStruct path, which is what LoadConfig uses).
func TestExpandEnv_NilBackendsMap(t *testing.T) {
	cfg := Config{Backends: nil, Registry: "test"}
	err := expandEnvInStruct(&cfg)
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Registry)
}

func TestLoadConfig_HappyPath(t *testing.T) {
	defer ResetForTesting()
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	cfgContent := `{
		"defaultBackend": "prod",
		"registry": "my-registry",
		"driverTimeout": "2m",
		"s3": {
			"region": "us-east-1",
			"endpoint": "https://s3.example.com"
		},
		"backends": {
			"prod": {
				"driver": "/usr/local/bin/coach-scaleway",
				"config": {"project": "my-proj"},
				"platform": "linux/amd64"
			}
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "coach.json"), []byte(cfgContent), 0o644))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "prod", cfg.DefaultBackend)
	assert.Equal(t, "my-registry", cfg.Registry)
	assert.Equal(t, "us-east-1", cfg.S3.Region)
	assert.Equal(t, "https://s3.example.com", cfg.S3.Endpoint)
	assert.Equal(t, 2*time.Minute, cfg.DriverTimeoutDuration())

	b, err := cfg.Backend("prod")
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/coach-scaleway", b.Driver)
	assert.JSONEq(t, `{"project":"my-proj"}`, string(b.Config))
}

func TestUnmarshalJSON_Invalid(t *testing.T) {
	var s SecureString
	err := s.UnmarshalJSON([]byte(`invalid`))
	require.Error(t, err)
}

func TestLoadConfigNotFound(t *testing.T) {
	defer ResetForTesting()
	// Ensure no coach.json in cwd or home
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	_, err := LoadConfig()
	require.Error(t, err)
	assert.ErrorIs(t, err, errConfigNotFound)
}
