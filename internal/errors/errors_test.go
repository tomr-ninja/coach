package errors_test

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
)

func TestNew(t *testing.T) {
	err := coacherrors.New(coacherrors.KindUser, "bad args")
	assert.Equal(t, coacherrors.KindUser, err.Kind)
	assert.Equal(t, "bad args", err.Error())
	assert.Nil(t, err.Cause)
	assert.Empty(t, err.Suggest)
	assert.False(t, err.Transient)
}

func TestWrap(t *testing.T) {
	cause := fmt.Errorf("io error")
	err := coacherrors.Wrap(cause, coacherrors.KindIO, "docker failed")

	assert.Equal(t, "docker failed", err.Error())
	assert.ErrorIs(t, err, cause)
}

func TestWithHint(t *testing.T) {
	err := coacherrors.New(coacherrors.KindUser, "artifact exists")
	err = coacherrors.WithHint(err, "Use --force to overwrite")
	assert.Equal(t, "Use --force to overwrite", err.Suggest)

	raw := fmt.Errorf("something failed")
	hinted := coacherrors.WithHint(raw, "try again")
	assert.Equal(t, "something failed", hinted.Error())
	assert.Equal(t, "try again", hinted.Suggest)
}

func TestAsTransient(t *testing.T) {
	err := coacherrors.New(coacherrors.KindIO, "timeout")
	err = coacherrors.AsTransient(err)
	assert.True(t, err.Transient)

	raw := fmt.Errorf("network error")
	trans := coacherrors.AsTransient(raw)
	assert.True(t, trans.Transient)
}

func TestGetKind(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want coacherrors.Kind
	}{
		{"typed", coacherrors.New(coacherrors.KindUser, "x"), coacherrors.KindUser},
		{"wrapped typed", fmt.Errorf("wrap: %w", coacherrors.New(coacherrors.KindIO, "x")), coacherrors.KindIO},
		{"raw fmt.Errorf", fmt.Errorf("plain"), coacherrors.KindInternal},
		{"nil", nil, coacherrors.KindInternal},
		{"nested chain", coacherrors.Wrap(
			coacherrors.New(coacherrors.KindDriver, "inner"),
			coacherrors.KindIO,
			"outer",
		), coacherrors.KindIO},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, coacherrors.GetKind(tt.err))
		})
	}
}

func TestGetHint(t *testing.T) {
	err := coacherrors.WithHint(coacherrors.New(coacherrors.KindUser, "x"), "do this")
	assert.Equal(t, "do this", coacherrors.GetHint(err))

	wrapped := fmt.Errorf("wrap: %w", err)
	assert.Equal(t, "do this", coacherrors.GetHint(wrapped))

	assert.Empty(t, coacherrors.GetHint(fmt.Errorf("plain")))
}

func TestIsTransient(t *testing.T) {
	err := coacherrors.AsTransient(coacherrors.New(coacherrors.KindIO, "timeout"))
	assert.True(t, coacherrors.IsTransient(err))

	wrapped := fmt.Errorf("wrap: %w", err)
	assert.True(t, coacherrors.IsTransient(wrapped))

	assert.False(t, coacherrors.IsTransient(fmt.Errorf("plain")))
}

func TestFormat(t *testing.T) {
	err := coacherrors.New(coacherrors.KindUser, "artifact already exists")
	assert.Equal(t, "artifact already exists", err.Format())

	err = coacherrors.WithHint(err, "Use --force to overwrite")
	assert.Equal(t, "artifact already exists\nHint: Use --force to overwrite", err.Format())
}

func TestDebugFormat(t *testing.T) {
	cause := coacherrors.New(coacherrors.KindDriver, "driver crashed")
	wrapped := coacherrors.Wrap(cause, coacherrors.KindIO, "container failed")
	wrapped.Suggest = "check Docker daemon"
	wrapped.Transient = true

	got := coacherrors.DebugFormat(wrapped)
	assert.Contains(t, got, "container failed")
	assert.Contains(t, got, "[Hint: check Docker daemon]")
	assert.Contains(t, got, "[transient]")
	assert.Contains(t, got, "driver crashed")
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		kind coacherrors.Kind
		want int
	}{
		{coacherrors.KindUser, 2},
		{coacherrors.KindIO, 3},
		{coacherrors.KindDriver, 4},
		{coacherrors.KindInternal, 1},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, coacherrors.ExitCode(tt.kind))
	}
}

func TestErrorsIsCompatibility(t *testing.T) {
	base := coacherrors.New(coacherrors.KindUser, "base")
	wrapped := fmt.Errorf("wrapped: %w", base)
	assert.ErrorIs(t, wrapped, base)
}

func TestErrorsAsCompatibility(t *testing.T) {
	base := coacherrors.New(coacherrors.KindUser, "base")
	wrapped := fmt.Errorf("wrapped: %w", base)

	var target *coacherrors.Error
	require.True(t, errors.As(wrapped, &target))
	assert.Equal(t, coacherrors.KindUser, target.Kind)
}

func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		transient bool
	}{
		{"nil", nil, false},
		{"fmt.Errorf plain", fmt.Errorf("plain"), false},
		{"net.DNSError", &net.DNSError{Name: "bad", Err: "no such host"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.transient, coacherrors.IsNetworkError(tt.err))
			assert.Equal(t, tt.transient, coacherrors.IsTransient(tt.err))
		})
	}
}
