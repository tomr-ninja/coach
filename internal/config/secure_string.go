package config

import (
	"encoding/json"
	"fmt"
)

// SecureString wraps a sensitive string value and redacts it in logs
// and fmt output. Use Reveal() only when the actual value is needed
// (e.g. passing to an API or setting an environment variable).
type SecureString struct {
	value string
}

// NewSecureString creates a SecureString from a plain string.
func NewSecureString(s string) SecureString {
	return SecureString{value: s}
}

// String returns a redacted placeholder. This ensures SecureString
// never leaks through fmt.Errorf, log output, or any %s/%v format verb.
func (s SecureString) String() string {
	if s.value == "" {
		return ""
	}
	return "[REDACTED]"
}

// GoString returns the same redacted form for %#v formatting.
func (s SecureString) GoString() string {
	return s.String()
}

// Reveal returns the actual sensitive value. Only call this at the
// point where the value is needed for its intended purpose.
func (s SecureString) Reveal() string {
	return s.value
}

// SetValue sets the underlying value from a plain string.
// This is used internally for env-var expansion.
func (s *SecureString) SetValue(v string) {
	s.value = v
}

// IsEmpty reports whether the underlying value is empty.
func (s SecureString) IsEmpty() bool {
	return s.value == ""
}

// MarshalJSON serializes the actual value as a JSON string.
func (s SecureString) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.value)
}

// UnmarshalJSON deserializes a JSON string into the SecureString.
func (s *SecureString) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	s.value = v
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (s SecureString) MarshalText() ([]byte, error) {
	return []byte(s.value), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *SecureString) UnmarshalText(text []byte) error {
	s.value = string(text)
	return nil
}

// Format implements fmt.Formatter so that %s, %v, %q all use the
// redacted String() representation.
func (s SecureString) Format(f fmt.State, verb rune) {
	switch verb {
	case 's', 'v', 'q':
		_, _ = fmt.Fprint(f, s.String())
	default:
		_, _ = fmt.Fprintf(f, "%%!%c(SecureString=%s)", verb, s.String())
	}
}
