// Package utils provides small shared helpers used across coach packages.
package utils

import "context"

type ctxKey string

const verboseKey ctxKey = "coach-verbose"

// WithVerbose returns a context marked as verbose.
func WithVerbose(ctx context.Context) context.Context {
	return context.WithValue(ctx, verboseKey, true)
}

// IsVerbose reports whether the context has verbose mode enabled.
func IsVerbose(ctx context.Context) bool {
	v, _ := ctx.Value(verboseKey).(bool)
	return v
}
