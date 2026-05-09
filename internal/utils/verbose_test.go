package utils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithVerbose(t *testing.T) {
	ctx := context.Background()
	vCtx := WithVerbose(ctx)
	assert.True(t, IsVerbose(vCtx))
	assert.False(t, IsVerbose(ctx), "original context should not be modified")
}

func TestIsVerbose(t *testing.T) {
	assert.False(t, IsVerbose(context.Background()))

	ctx := WithVerbose(context.Background())
	assert.True(t, IsVerbose(ctx))

	// nil context should not panic
	assert.False(t, IsVerbose(context.TODO()))
}
