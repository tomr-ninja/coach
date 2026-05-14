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

// smithyError constructs a smithy http.ResponseError for table-driven tests.
func smithyError(code int, msg string) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: code}},
		Err:      errors.New(msg),
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"smithy 404", smithyError(404, "not found"), true},
		{"smithy 403", smithyError(403, "forbidden"), false},
		{"smithy 200", smithyError(200, "ok"), false},
		{"plain error with 404", errors.New("got 404 response"), true},
		{"plain error with NotFound", errors.New("object NotFound"), true},
		{"plain error with NoSuchKey", errors.New("NoSuchKey: the specified key does not exist"), true},
		{"plain error without clues", errors.New("connection timeout"), false},
		{"wrapped smithy 404", fmt.Errorf("head object failed: %w", smithyError(404, "not found")), true},
		{"wrapped smithy 403", fmt.Errorf("head object failed: %w", smithyError(403, "forbidden")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNotFound(tt.err))
		})
	}
}
