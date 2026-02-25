package adapters_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/suarezc/errata/internal/tools"
)

func TestSeedContext_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// Before setting, should not be present.
	_, ok := tools.SeedFromContext(ctx)
	assert.False(t, ok, "seed should not be set on bare context")

	// Set seed and read it back.
	ctx = tools.WithSeed(ctx, 42)
	seed, ok := tools.SeedFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, int64(42), seed)
}

func TestSeedContext_Zero(t *testing.T) {
	ctx := tools.WithSeed(context.Background(), 0)
	seed, ok := tools.SeedFromContext(ctx)
	assert.True(t, ok, "seed 0 should be distinguishable from not-set")
	assert.Equal(t, int64(0), seed)
}

func TestSeedContext_Negative(t *testing.T) {
	ctx := tools.WithSeed(context.Background(), -1)
	seed, ok := tools.SeedFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, int64(-1), seed)
}
