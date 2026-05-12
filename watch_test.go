package coach

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
)

func TestParseS3LogURI(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		wantBucket string
		wantKey    string
	}{
		{"standard s3 uri", "s3://mybucket/logs/log.txt", "mybucket", "logs/log.txt"},
		{"bucket only (no key)", "s3://mybucket", "mybucket", ""},
		{"empty string", "", "", ""},
		{"not s3 prefix (paths.TrimPrefix keeps rest)", "/local/path/log.txt", "", "local/path/log.txt"},
		{"deeply nested key", "s3://bucket/a/b/c/d/log.txt", "bucket", "a/b/c/d/log.txt"},
		{"key with special chars", "s3://bucket/logs/my-model@v1.0/log.txt", "bucket", "logs/my-model@v1.0/log.txt"},
		{"triple slash", "s3://bucket///logs/log.txt", "bucket", "//logs/log.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, key := parseS3LogURI(tt.uri)
			assert.Equal(t, tt.wantBucket, bucket, "bucket mismatch")
			assert.Equal(t, tt.wantKey, key, "key mismatch")
		})
	}
}

func TestIsNotFound(t *testing.T) {
	t.Run("smithy ResponseError with 404", func(t *testing.T) {
		respErr := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
			Err:      errors.New("not found"),
		}
		assert.True(t, isNotFound(respErr))
	})

	t.Run("smithy ResponseError with 403", func(t *testing.T) {
		respErr := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 403}},
			Err:      errors.New("forbidden"),
		}
		assert.False(t, isNotFound(respErr))
	})

	t.Run("smithy ResponseError with 200", func(t *testing.T) {
		respErr := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 200}},
			Err:      errors.New("ok"),
		}
		assert.False(t, isNotFound(respErr))
	})

	t.Run("plain error containing 404", func(t *testing.T) {
		assert.True(t, isNotFound(errors.New("got 404 response")))
	})

	t.Run("plain error containing NotFound", func(t *testing.T) {
		assert.True(t, isNotFound(errors.New("object NotFound")))
	})

	t.Run("plain error containing NoSuchKey", func(t *testing.T) {
		assert.True(t, isNotFound(errors.New("NoSuchKey: the specified key does not exist")))
	})

	t.Run("plain error without not-found clues", func(t *testing.T) {
		assert.False(t, isNotFound(errors.New("connection timeout")))
	})

	t.Run("wrapped smithy error with 404", func(t *testing.T) {
		respErr := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
			Err:      errors.New("not found"),
		}
		wrapped := fmt.Errorf("head object failed: %w", respErr)
		assert.True(t, isNotFound(wrapped))
	})

	t.Run("wrapped smithy error with 403", func(t *testing.T) {
		respErr := &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 403}},
			Err:      errors.New("forbidden"),
		}
		wrapped := fmt.Errorf("head object failed: %w", respErr)
		assert.False(t, isNotFound(wrapped))
	})
}
