package config

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecureString_String_Redacts(t *testing.T) {
	s := NewSecureString("secret-value")
	assert.Equal(t, "[REDACTED]", s.String())
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%s", s))
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%v", s))
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%q", s))
}

func TestSecureString_String_Empty(t *testing.T) {
	var s SecureString
	assert.Equal(t, "", s.String())
}

func TestSecureString_Reveal(t *testing.T) {
	s := NewSecureString("secret-value")
	assert.Equal(t, "secret-value", s.Reveal())
}

func TestSecureString_Reveal_Empty(t *testing.T) {
	var s SecureString
	assert.Equal(t, "", s.Reveal())
}

func TestSecureString_IsEmpty(t *testing.T) {
	var s SecureString
	assert.True(t, s.IsEmpty())

	s = NewSecureString("x")
	assert.False(t, s.IsEmpty())
}

func TestSecureString_SetValue(t *testing.T) {
	var s SecureString
	s.SetValue("new-val")
	assert.Equal(t, "new-val", s.Reveal())
	assert.Equal(t, "[REDACTED]", s.String())
}

func TestSecureString_MarshalJSON(t *testing.T) {
	s := NewSecureString("my-secret")
	data, err := s.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `"my-secret"`, string(data))
}

func TestSecureString_UnmarshalJSON(t *testing.T) {
	var s SecureString
	err := s.UnmarshalJSON([]byte(`"my-secret"`))
	require.NoError(t, err)
	assert.Equal(t, "my-secret", s.Reveal())
	assert.Equal(t, "[REDACTED]", s.String())
}

func TestSecureString_MarshalJSON_Empty(t *testing.T) {
	var s SecureString
	data, err := s.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `""`, string(data))
}

func TestSecureString_MarshalText(t *testing.T) {
	s := NewSecureString("my-secret")
	data, err := s.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "my-secret", string(data))
}

func TestSecureString_UnmarshalText(t *testing.T) {
	var s SecureString
	err := s.UnmarshalText([]byte("my-secret"))
	require.NoError(t, err)
	assert.Equal(t, "my-secret", s.Reveal())
}

func TestSecureString_GoString(t *testing.T) {
	s := NewSecureString("secret-value")
	assert.Equal(t, "[REDACTED]", s.GoString())
}

func TestSecureString_Format(t *testing.T) {
	s := NewSecureString("secret")
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%s", s))
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%v", s))
	assert.Equal(t, "[REDACTED]", fmt.Sprintf("%q", s))
	assert.Equal(t, "%!d(SecureString=[REDACTED])", fmt.Sprintf("%d", s))
}

func TestSecureString_JSON_Roundtrip(t *testing.T) {
	original := NewSecureString("hello-world")
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded SecureString
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Reveal(), decoded.Reveal())
	assert.True(t, decoded.String() == "[REDACTED]", "decoded String should be redacted")
}
