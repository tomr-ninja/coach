package coach

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
)

var (
	errConfigNotFound = errors.New("coach.json not found in current directory or ~/.config/coach/")
	errNoBackend      = coacherrors.WithHint(
		coacherrors.New(coacherrors.KindUser, "no backend configured"),
		"Add a backend to coach.json and set defaultBackend, or pass --backend",
	)
	errBackendNotFound = coacherrors.New(coacherrors.KindUser, "backend not found in config")
	errEnvVarNotSet    = coacherrors.New(coacherrors.KindUser, "environment variable not set")
	errUnsupportedType = errors.New("expandEnvInValue: unsupported type")
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

func buildS3EnvVars(s3 S3Config) map[string]string {
	provider := s3.Provider
	if provider == "" {
		provider = "AWS"
	}
	return map[string]string{
		"RCLONE_CONFIG_S3_TYPE":              "s3",
		"RCLONE_CONFIG_S3_PROVIDER":          provider,
		"RCLONE_CONFIG_S3_ACCESS_KEY_ID":     s3.AccessKeyID,
		"RCLONE_CONFIG_S3_SECRET_ACCESS_KEY": s3.SecretAccessKey,
		"RCLONE_CONFIG_S3_REGION":            s3.Region,
		"RCLONE_CONFIG_S3_ENDPOINT":          s3.Endpoint,
	}
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
		if err = json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		for name, backend := range cfg.Backends {
			expanded, err := expandEnvInConfig(backend.Config)
			if err != nil {
				return nil, fmt.Errorf("expand backend %q config in %s: %w", name, p, err)
			}
			cfg.Backends[name] = Backend{Driver: backend.Driver, Config: expanded}
		}

		if err := expandEnvInStruct(&cfg); err != nil {
			return nil, fmt.Errorf("expand config in %s: %w", p, err)
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
		names := make([]string, 0, len(c.Backends))
		for k := range c.Backends {
			names = append(names, k)
		}
		err := coacherrors.Wrap(errBackendNotFound, coacherrors.KindUser,
			fmt.Sprintf("backend %q not found in coach.json", name))
		if len(names) > 0 {
			err.Suggest = fmt.Sprintf("Available backends: %s", strings.Join(names, ", "))
		}
		return nil, err
	}
	return &b, nil
}

var envVarPattern = regexp.MustCompile(`"\$[A-Za-z_][A-Za-z0-9_]*"`)

func expandEnvString(s string) (string, error) {
	if s == "" || s[0] != '$' {
		return s, nil
	}

	name := s[1:]
	val, ok := os.LookupEnv(name)
	if !ok {
		return "", coacherrors.WithHint(
			fmt.Errorf("%w: %q", errEnvVarNotSet, name),
			fmt.Sprintf("Export %s=... before running coach, or replace \"$%s\" with a literal value in coach.json", name, name),
		)
	}

	return val, nil
}

func expandEnvInStruct(v any) error {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Ptr || val.IsNil() {
		return nil
	}
	val = val.Elem()
	if val.Kind() != reflect.Struct {
		return nil
	}

	return expandEnvInValue(val)
}

func expandEnvInValue(val reflect.Value) error {
	switch val.Kind() {
	case reflect.Struct:
		for _, field := range val.Fields() {
			if field.CanSet() {
				if err := expandEnvInValue(field); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		if val.IsNil() {
			return nil
		}
		iter := val.MapRange()
		for iter.Next() {
			elem := iter.Value()
			if elem.Kind() == reflect.Struct && elem.CanAddr() {
				if err := expandEnvInValue(elem.Addr()); err != nil {
					return err
				}
			}
		}
	case reflect.String:
		expanded, err := expandEnvString(val.String())
		if err != nil {
			return err
		}
		val.SetString(expanded)
	default:
		return fmt.Errorf("%w: %v", errUnsupportedType, val.Kind())
	}

	return nil
}

func expandEnvInConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var expandErr error
	result := envVarPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		if expandErr != nil {
			return match
		}
		name := string(match[2 : len(match)-1])
		val, ok := os.LookupEnv(name)
		if !ok {
			expandErr = coacherrors.WithHint(
				fmt.Errorf("%w: %q", errEnvVarNotSet, name),
				fmt.Sprintf("Export %s=... before running coach, or replace \"$%s\" with a literal value in coach.json", name, name),
			)
			return match
		}
		b, _ := json.Marshal(val)

		return b
	})
	if expandErr != nil {
		return nil, expandErr
	}

	return result, nil
}
