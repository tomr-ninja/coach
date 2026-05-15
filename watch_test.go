package coach

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
)

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
		{"plain error (no ResponseError wrapping)", errors.New("got 404 response"), false},
		{"wrapped smithy 404", fmt.Errorf("head object failed: %w", smithyError(404, "not found")), true},
		{"wrapped smithy 403", fmt.Errorf("head object failed: %w", smithyError(403, "forbidden")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNotFound(tt.err))
		})
	}
}
