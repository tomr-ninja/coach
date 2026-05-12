package coach

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithVerbose(t *testing.T) {
	ctx := context.Background()

	// Background context is not verbose by default.
	assert.False(t, IsVerbose(ctx))

	// Marking it as verbose should be detectable.
	vctx := WithVerbose(ctx)
	assert.True(t, IsVerbose(vctx))
}

func TestIsVerbose_NonVerbose(t *testing.T) {
	assert.False(t, IsVerbose(context.Background()))
	assert.False(t, IsVerbose(context.TODO()))
}
