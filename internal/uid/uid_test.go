package uid_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/errata-app/errata-cli/internal/uid"
)

// uuidV7Re matches a UUID v7: version nibble is 7, variant bits are 8-b.
var uuidV7Re = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

func TestNew_Format(t *testing.T) {
	id := uid.New("ses_")
	require.Greater(t, len(id), 4)
	assert.Equal(t, "ses_", id[:4])
	assert.Regexp(t, uuidV7Re, id[4:])
}

func TestNew_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for range 100 {
		id := uid.New("x_")
		assert.False(t, seen[id], "duplicate ID: %s", id)
		seen[id] = true
	}
}

func TestNew_Monotonic(t *testing.T) {
	prev := uid.New("t_")
	for range 50 {
		next := uid.New("t_")
		// UUID v7 embeds a timestamp in the first 48 bits; lexicographic
		// ordering of the UUID portion (after the prefix) must be
		// non-decreasing within a single goroutine.
		assert.GreaterOrEqual(t, next[2:], prev[2:],
			"expected monotonic ordering: %s >= %s", next, prev)
		prev = next
	}
}
