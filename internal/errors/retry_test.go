package errors_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	coacherrors "github.com/tomr-ninja/coach/internal/errors"
)

func TestRetrySuccess(t *testing.T) {
	val, err := coacherrors.Retry(context.Background(), coacherrors.RetryConfig{}, func() (string, error) {
		return "done", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "done", val)
}

func TestRetryTransientSucceedsOnRetry(t *testing.T) {
	calls := 0
	val, err := coacherrors.Retry(context.Background(), coacherrors.RetryConfig{}, func() (int, error) {
		calls++
		if calls < 3 {
			return 0, coacherrors.AsTransient(fmt.Errorf("temporary error %d", calls))
		}
		return 42, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 42, val)
	assert.Equal(t, 3, calls)
}

func TestRetryNonTransientStopsImmediately(t *testing.T) {
	calls := 0
	_, err := coacherrors.Retry(context.Background(), coacherrors.RetryConfig{}, func() (struct{}, error) {
		calls++
		return struct{}{}, coacherrors.New(coacherrors.KindUser, "permanent error")
	})
	assert.Error(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetryRawErrorStopsImmediately(t *testing.T) {
	calls := 0
	_, err := coacherrors.Retry(context.Background(), coacherrors.RetryConfig{}, func() (struct{}, error) {
		calls++
		return struct{}{}, fmt.Errorf("raw error")
	})
	assert.Error(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetryConfigRetryAll(t *testing.T) {
	calls := 0
	val, err := coacherrors.Retry(context.Background(), coacherrors.RetryConfig{RetryAll: true}, func() (int, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("non-transient error")
		}
		return 7, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 7, val)
	assert.Equal(t, 3, calls)
}

func TestRetryConfigMaxRetries(t *testing.T) {
	calls := 0
	transientErr := coacherrors.AsTransient(fmt.Errorf("always fails"))

	_, err := coacherrors.Retry(context.Background(), coacherrors.RetryConfig{MaxRetries: 2}, func() (struct{}, error) {
		calls++
		return struct{}{}, transientErr
	})
	assert.Error(t, err)
	assert.Equal(t, 3, calls)
}

func TestRetryContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	done := make(chan error, 1)
	go func() {
		_, err := coacherrors.Retry(ctx, coacherrors.RetryConfig{}, func() (struct{}, error) {
			calls++
			time.Sleep(50 * time.Millisecond)
			return struct{}{}, coacherrors.AsTransient(fmt.Errorf("error"))
		})
		done <- err
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()

	err := <-done
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRetryWrappedTransient(t *testing.T) {
	calls := 0
	transient := coacherrors.AsTransient(fmt.Errorf("transient"))

	val, err := coacherrors.Retry(context.Background(), coacherrors.RetryConfig{}, func() (string, error) {
		calls++
		if calls < 2 {
			return "", fmt.Errorf("outer: %w", transient)
		}
		return "ok", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "ok", val)
	assert.Equal(t, 2, calls)
}
