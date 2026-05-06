package coach

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeImageName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "myimage", "myimage"},
		{"with registry", "registry.io/user/myimage", "myimage"},
		{"with tag", "myimage:v1.0", "myimage-v1.0"},
		{"with registry and tag", "registry.io/user/myimage:v1.0", "myimage-v1.0"},
		{"unsafe chars", "my/image@sha256:abc", "image-sha256-abc"},
		{"multiple slashes", "a/b/c/d", "d"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeImageName(tt.input), "input: %q", tt.input)
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "''"},
		{"simple", "hello", "hello"},
		{"with space", "hello world", "'hello world'"},
		{"with single quote", "it's", "'it'\\''s'"},
		{"with unsafe", "foo;bar", "'foo;bar'"},
		{"safe path", "/data/input.txt", "/data/input.txt"},
		{"mixed", "foo_bar-baz.test", "foo_bar-baz.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shellQuote(tt.input), "input: %q", tt.input)
		})
	}
}

func TestShellSafe(t *testing.T) {
	tests := []struct {
		char rune
		want bool
	}{
		{'a', true},
		{'Z', true},
		{'5', true},
		{'-', true},
		{'_', true},
		{'.', true},
		{'/', true},
		{',', true},
		{' ', false},
		{';', false},
		{'\'', false},
		{'$', false},
		{'\n', false},
	}

	for _, tt := range tests {
		t.Run(string(tt.char), func(t *testing.T) {
			assert.Equal(t, tt.want, shellSafe(tt.char), "char: %q", tt.char)
		})
	}
}
