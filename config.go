package coach

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var (
	errConfigNotFound  = errors.New("coach.json not found in current directory or ~/.config/coach/")
	errNoBackend       = errors.New("no backend specified and no defaultBackend configured")
	errBackendNotFound = errors.New("backend not found in config")
)

type S3Config struct {
	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	Region          string `json:"region,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	Provider        string `json:"provider,omitempty"`
}

type Config struct {
	Backends       map[string]Backend `json:"backends"`
	DefaultBackend string             `json:"defaultBackend,omitempty"`
	S3             S3Config           `json:"s3"`
	Registry       string             `json:"registry,omitempty"`
	RegistryAuth   string             `json:"registryAuth,omitempty"`
}

type Backend struct {
	Driver string          `json:"driver"`
	Config json.RawMessage `json:"config,omitempty"`
}

func LoadConfig() (*Config, error) {
	paths := []string{"coach.json"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "coach", "coach.json"))
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var cfg Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		for name, backend := range cfg.Backends {
			expanded := expandEnvInConfig(backend.Config)
			cfg.Backends[name] = Backend{Driver: backend.Driver, Config: expanded}
		}
		if s3Raw, err := json.Marshal(cfg.S3); err == nil {
			expanded := expandEnvInConfig(s3Raw)
			_ = json.Unmarshal(expanded, &cfg.S3)
		}
		return &cfg, nil
	}

	return nil, errConfigNotFound
}

func (c *Config) Backend(name string) (*Backend, error) {
	if name == "" {
		name = c.DefaultBackend
	}
	if name == "" {
		return nil, errNoBackend
	}
	b, ok := c.Backends[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", errBackendNotFound, name)
	}
	return &b, nil
}

var envVarPattern = regexp.MustCompile(`"\$[A-Za-z_][A-Za-z0-9_]*"`)

func expandEnvInConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	result := envVarPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		name := string(match[2 : len(match)-1])
		val, ok := os.LookupEnv(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: environment variable %q is not set, using empty string\n", name)
			return []byte(`""`)
		}
		b, _ := json.Marshal(val)
		return b
	})
	return result
}
