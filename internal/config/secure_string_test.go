package config

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecureString(t *testing.T) {
	// Redaction via String(), GoString(), and fmt verbs.
	s := NewSecureString("secret-value")
	assert.Equal(t, "[REDACTED]", s.String())
	assert.Equal(t, "[REDACTED]", s.GoString())
	assert.Equal(t, "[REDACTED]", s.String())
	assert.Equal(t, "[REDACTED]", s.String())
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%q", s))
	assert.Equal(t, "%!d(SecureString=[REDACTED])", fmt.Sprintf("%d", s))

	// Empty SecureString returns empty string, not "[REDACTED]".
	var empty SecureString
	assert.Equal(t, "", empty.String())

	// Reveal returns the raw value.
	assert.Equal(t, "secret-value", s.Reveal())
	assert.Equal(t, "", empty.Reveal())

	// IsEmpty.
	assert.True(t, empty.IsEmpty())
	assert.False(t, s.IsEmpty())

	// SetValue.
	var mutable SecureString
	mutable.SetValue("new-val")
	assert.Equal(t, "new-val", mutable.Reveal())
	assert.Equal(t, "[REDACTED]", mutable.String())
}

func TestSecureString_JSON(t *testing.T) {
	// Marshal / unmarshal non-empty.
	s := NewSecureString("my-secret")
	data, err := s.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `"my-secret"`, string(data))

	var decoded SecureString
	err = decoded.UnmarshalJSON([]byte(`"my-secret"`))
	require.NoError(t, err)
	assert.Equal(t, "my-secret", decoded.Reveal())
	assert.Equal(t, "[REDACTED]", decoded.String())

	// Marshal empty.
	var empty SecureString
	data, err = empty.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `""`, string(data))

	// Full roundtrip through encoding/json.
	original := NewSecureString("hello-world")
	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var rt SecureString
	err = json.Unmarshal(raw, &rt)
	require.NoError(t, err)
	assert.Equal(t, original.Reveal(), rt.Reveal())
	assert.Equal(t, "[REDACTED]", rt.String())
}

func TestSecureString_Text(t *testing.T) {
	// Marshal / unmarshal text.
	s := NewSecureString("my-secret")
	data, err := s.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "my-secret", string(data))

	var decoded SecureString
	err = decoded.UnmarshalText([]byte("my-secret"))
	require.NoError(t, err)
	assert.Equal(t, "my-secret", decoded.Reveal())
}
