package wrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomSuffix(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		s := randomSuffix()
		assert.Len(t, s, 12, "suffix length")
		for _, c := range s {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"invalid hex char %q in suffix %q", c, s)
		}
		assert.False(t, seen[s], "duplicate suffix %q — collision within 100 iterations", s)
		seen[s] = true
	}
}
