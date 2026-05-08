package coach

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tomr-ninja/coach/internal/validate"
	"github.com/tomr-ninja/coach/protocol"
)

func TestValidateSubmitResult(t *testing.T) {
	t.Run("valid result", func(t *testing.T) {
		err := validate.SubmitResult(&protocol.SubmitResult{ID: "job-123"})
		require.NoError(t, err)
	})

	t.Run("empty ID", func(t *testing.T) {
		err := validate.SubmitResult(&protocol.SubmitResult{ID: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty ID")
	})

	t.Run("nil result handled by caller", func(t *testing.T) {
		// Caller checks for nil before calling validate.SubmitResult.
		// But if they don't, the function will panic (as expected for a programmer error).
		assert.Panics(t, func() {
			validate.SubmitResult(nil)
		})
	})
}

func TestValidateStatusResult(t *testing.T) {
	t.Run("valid result", func(t *testing.T) {
		err := validate.StatusResult(&protocol.StatusResult{ID: "job-123", State: "running"})
		require.NoError(t, err)
	})

	t.Run("empty ID", func(t *testing.T) {
		err := validate.StatusResult(&protocol.StatusResult{ID: "", State: "running"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty ID")
	})

	t.Run("empty State", func(t *testing.T) {
		err := validate.StatusResult(&protocol.StatusResult{ID: "job-123", State: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty State")
	})

	t.Run("both empty", func(t *testing.T) {
		err := validate.StatusResult(&protocol.StatusResult{ID: "", State: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty ID")
	})
}
