package coach

import (
	"context"

	"github.com/tomr-ninja/coach/docker"
)

type ctxKey string

const verboseKey ctxKey = "coach-verbose"

// WithVerbose returns a context marked as verbose for coach and Docker operations.
func WithVerbose(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, verboseKey, true)
	return docker.WithVerbose(ctx)
}

// IsVerbose reports whether the context has verbose mode enabled.
func IsVerbose(ctx context.Context) bool {
	v, _ := ctx.Value(verboseKey).(bool)
	return v
}
