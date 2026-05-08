package coach

import (
	"context"

	"github.com/tomr-ninja/coach/internal/utils"
)

// WithVerbose returns a context marked as verbose for coach operations.
func WithVerbose(ctx context.Context) context.Context {
	return utils.WithVerbose(ctx)
}

// IsVerbose reports whether the context has verbose mode enabled.
func IsVerbose(ctx context.Context) bool {
	return utils.IsVerbose(ctx)
}
