package errors

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v5"
)

const (
	defaultMaxRetries = 3
	defaultMaxElapsed = 15 * time.Second
)

// RetryConfig controls retry behaviour for Retry.
// The zero value is usable and applies sensible defaults.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (default 3).
	MaxRetries uint

	// MaxElapsed is the maximum total wall-clock time for all retries (default 15s).
	MaxElapsed time.Duration

	// RetryAll retries on any error instead of only transient ones.
	RetryAll bool
}

// Retry runs operation with exponential backoff. Only transient errors
// (as determined by IsTransient) are retried by default; set cfg.RetryAll
// to force retries on any error.
func Retry[T any](ctx context.Context, cfg RetryConfig, operation func() (T, error)) (T, error) {
	maxRetries := cfg.MaxRetries
	if maxRetries == 0 {
		maxRetries = defaultMaxRetries
	}
	maxElapsed := cfg.MaxElapsed
	if maxElapsed == 0 {
		maxElapsed = defaultMaxElapsed
	}

	backoffOpts := []backoff.RetryOption{
		backoff.WithBackOff(backoff.NewExponentialBackOff()),
		backoff.WithMaxTries(maxRetries + 1), // +1 for the initial attempt
		backoff.WithMaxElapsedTime(maxElapsed),
	}

	return backoff.Retry(ctx, func() (T, error) {
		result, err := operation()
		if err == nil {
			return result, nil
		}
		if !cfg.RetryAll && !IsTransient(err) {
			return result, backoff.Permanent(err)
		}
		return result, err
	}, backoffOpts...)
}
