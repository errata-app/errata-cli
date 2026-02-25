package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetDynamicPricing clears the package-level pricing cache between tests
// so each test starts from a known state.
func resetDynamicPricing(t *testing.T) {
	t.Helper()
	pricingMu.Lock()
	dynamicPricing = nil
	pricingMu.Unlock()
}

// ─── CostUSD ─────────────────────────────────────────────────────────────────

func TestCostUSD_HardcodedModel(t *testing.T) {
	resetDynamicPricing(t)
	// claude-sonnet-4-6: $3.00 input / $15.00 output per million tokens
	cost := CostUSD("claude-sonnet-4-6", 1_000_000, 0, 0, 1_000_000)
	assert.InDelta(t, 18.0, cost, 0.001) // 3.00 + 15.00
}

func TestCostUSD_UnknownModel_ReturnsZero(t *testing.T) {
	resetDynamicPricing(t)
	assert.Equal(t, 0.0, CostUSD("no-such-model-xyz", 1_000_000, 0, 0, 1_000_000))
}

// TestCostUSD_QualifiedIDFallback verifies that "provider/model" strips the
// prefix and falls back to the bare model ID in the hardcoded table.
func TestCostUSD_QualifiedIDFallback(t *testing.T) {
	resetDynamicPricing(t)
	// "anthropic/claude-sonnet-4-6" is not in the hardcoded table, but "claude-sonnet-4-6" is.
	cost := CostUSD("anthropic/claude-sonnet-4-6", 1_000_000, 0, 0, 1_000_000)
	assert.InDelta(t, 18.0, cost, 0.001)
}

func TestCostUSD_ZeroTokens(t *testing.T) {
	resetDynamicPricing(t)
	assert.Equal(t, 0.0, CostUSD("claude-sonnet-4-6", 0, 0, 0, 0))
}

func TestCostUSD_OnlyInputTokens(t *testing.T) {
	resetDynamicPricing(t)
	// claude-sonnet-4-6: $3.00/M input
	cost := CostUSD("claude-sonnet-4-6", 1_000_000, 0, 0, 0)
	assert.InDelta(t, 3.0, cost, 0.001)
}

// ─── Cache-aware CostUSD ──────────────────────────────────────────────────────

// TestCostUSD_CacheReadDiscount verifies that cache-read tokens are charged at
// CacheReadPMT (10% of InputPMT for Anthropic) rather than the full InputPMT.
func TestCostUSD_CacheReadDiscount(t *testing.T) {
	resetDynamicPricing(t)
	// claude-sonnet-4-6: InputPMT=$3.00, CacheReadPMT=$0.30/M
	// 1M cache-read tokens at $0.30/M = $0.30
	cost := CostUSD("claude-sonnet-4-6", 0, 1_000_000, 0, 0)
	assert.InDelta(t, 0.30, cost, 0.0001)
}

// TestCostUSD_CacheWritePremium verifies that cache-creation tokens are charged
// at CacheWritePMT (125% of InputPMT for Anthropic).
func TestCostUSD_CacheWritePremium(t *testing.T) {
	resetDynamicPricing(t)
	// claude-sonnet-4-6: InputPMT=$3.00, CacheWritePMT=$3.75/M
	// 1M cache-creation tokens at $3.75/M = $3.75
	cost := CostUSD("claude-sonnet-4-6", 0, 0, 1_000_000, 0)
	assert.InDelta(t, 3.75, cost, 0.0001)
}

// TestCostUSD_CacheRead_LessThan_RegularInput verifies that a run with cache
// reads costs less than the same run charged entirely at the regular input rate.
func TestCostUSD_CacheRead_LessThan_RegularInput(t *testing.T) {
	resetDynamicPricing(t)
	// 1M regular + 1M cache-read for claude-sonnet-4-6
	costWithCache := CostUSD("claude-sonnet-4-6", 1_000_000, 1_000_000, 0, 0)
	// Same total input all at regular rate
	costWithout := CostUSD("claude-sonnet-4-6", 2_000_000, 0, 0, 0)
	assert.Less(t, costWithCache, costWithout, "cache reads should cost less than regular input")
}

// TestCostUSD_NoCacheRates_ChargesAllInputAtStandardRate verifies the fallback:
// when a model has no CacheReadPMT (e.g. dynamically fetched OpenRouter model),
// all input tokens (regular + cache) are charged at the standard InputPMT.
func TestCostUSD_NoCacheRates_ChargesAllInputAtStandardRate(t *testing.T) {
	resetDynamicPricing(t)
	// Inject a dynamic pricing entry with no cache rates (simulates OpenRouter fetch).
	pricingMu.Lock()
	dynamicPricing = map[string]modelPricing{
		"test/no-cache-model": {InputPMT: 2.0, OutputPMT: 8.0},
	}
	pricingMu.Unlock()
	defer resetDynamicPricing(t)

	// 1M regular + 500k cache-read: all charged at $2.00/M = $3.00 total input cost
	cost := CostUSD("test/no-cache-model", 1_000_000, 500_000, 0, 0)
	assert.InDelta(t, 3.0, cost, 0.0001, "all input tokens should be charged at InputPMT when no cache rates")
}

// TestCostUSD_ZeroCacheTokens_MatchesLegacyBehaviour verifies that passing
// zero cache tokens produces the same result as the pre-cache 3-param formula.
func TestCostUSD_ZeroCacheTokens_MatchesLegacyBehaviour(t *testing.T) {
	resetDynamicPricing(t)
	// gpt-4o: InputPMT=$2.50, OutputPMT=$10.00
	// Old formula: (1M * 2.50 + 1M * 10.00) / 1M = $12.50
	cost := CostUSD("gpt-4o", 1_000_000, 0, 0, 1_000_000)
	assert.InDelta(t, 12.50, cost, 0.0001)
}

// ─── readPricingCache ─────────────────────────────────────────────────────────

func TestReadPricingCache_MissingFile_ReturnsNil(t *testing.T) {
	result := readPricingCache(filepath.Join(t.TempDir(), "nonexistent.json"))
	assert.Nil(t, result)
}

// TestReadPricingCache_AcceptsValidPrices is a regression test.
// When modelPricing had unexported fields, encoding/json silently dropped
// the price values, causing every entry to read back as {0, 0} and the
// cache to be rejected. This test would fail under that bug.
func TestReadPricingCache_AcceptsValidPrices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	raw := `{"fetched_at":"2099-01-01T00:00:00Z","models":{"test-model":{"input_pmt":10.0,"output_pmt":30.0}}}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	c := readPricingCache(path)
	require.NotNil(t, c, "valid cache with non-zero prices should not be rejected")
	assert.InDelta(t, 10.0, c.Models["test-model"].InputPMT, 0.001)
	assert.InDelta(t, 30.0, c.Models["test-model"].OutputPMT, 0.001)
}

// TestReadPricingCache_RejectsAllZeroPrices verifies the zero-value guard.
// A cache where every entry has {0, 0} prices is treated as corrupt and
// returns nil so a fresh fetch is triggered.
func TestReadPricingCache_RejectsAllZeroPrices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	corrupt := `{"fetched_at":"2099-01-01T00:00:00Z","models":{"claude-sonnet-4-6":{"input_pmt":0,"output_pmt":0}}}`
	require.NoError(t, os.WriteFile(path, []byte(corrupt), 0o644))

	assert.Nil(t, readPricingCache(path), "all-zero prices should cause cache to be rejected")
}

// ─── LoadPricing round-trip ───────────────────────────────────────────────────

// TestLoadPricing_CacheRoundTrip is the key regression test.
// It writes a valid pricing cache to disk, calls LoadPricing, then checks
// that CostUSD returns the correct value — proving that prices survive the
// full write → disk → read → use path.
//
// Under the unexported-fields bug (pre-fix), this test would return 0 because
// encoding/json would fail to deserialize the prices, the zero-value guard
// would reject the cache, and the model ID is not in the hardcoded fallback table.
func TestLoadPricing_CacheRoundTrip(t *testing.T) {
	resetDynamicPricing(t)
	defer resetDynamicPricing(t)

	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "cache.json")

	// Write a cache file using a local struct that mirrors pricingCacheFile.
	// Use a model ID not in the hardcoded table so a 0 result unambiguously
	// means the cache was not read correctly.
	type testPrice struct {
		InputPMT  float64 `json:"input_pmt"`
		OutputPMT float64 `json:"output_pmt"`
	}
	type testCache struct {
		FetchedAt time.Time             `json:"fetched_at"`
		Models    map[string]testPrice  `json:"models"`
	}
	b, err := json.Marshal(testCache{
		FetchedAt: time.Now(),
		Models:    map[string]testPrice{"pricing-roundtrip-sentinel": {InputPMT: 7.0, OutputPMT: 21.0}},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cacheFile, b, 0o644))

	LoadPricing(cacheFile)

	// 1M input * $7/M + 1M output * $21/M = $28
	cost := CostUSD("pricing-roundtrip-sentinel", 1_000_000, 0, 0, 1_000_000)
	assert.InDelta(t, 28.0, cost, 0.001,
		"prices must survive the cache round-trip; "+
			"if this returns 0, modelPricing fields may be unexported or missing json tags")
}

// ─── ContextWindowTokens ──────────────────────────────────────────────────────

func TestContextWindowTokens_KnownModel(t *testing.T) {
	resetDynamicPricing(t)
	cw := ContextWindowTokens("claude-sonnet-4-6")
	assert.Equal(t, int64(200_000), cw)
}

func TestContextWindowTokens_UnknownModel(t *testing.T) {
	resetDynamicPricing(t)
	assert.Equal(t, int64(0), ContextWindowTokens("no-such-model-xyz"))
}

func TestContextWindowTokens_QualifiedID(t *testing.T) {
	resetDynamicPricing(t)
	// "anthropic/claude-sonnet-4-6" strips to "claude-sonnet-4-6" in the hardcoded table.
	assert.Equal(t, int64(200_000), ContextWindowTokens("anthropic/claude-sonnet-4-6"))
}

func TestContextWindowTokens_Gemini(t *testing.T) {
	resetDynamicPricing(t)
	cw := ContextWindowTokens("gemini-2.0-flash")
	assert.Equal(t, int64(1_000_000), cw)
}

// ─── ProviderQualifiedID ──────────────────────────────────────────────────────

func TestProviderQualifiedID_Anthropic(t *testing.T) {
	assert.Equal(t, "anthropic/claude-sonnet-4-6", ProviderQualifiedID("Anthropic", "claude-sonnet-4-6"))
}

func TestProviderQualifiedID_OpenAI(t *testing.T) {
	assert.Equal(t, "openai/gpt-4o", ProviderQualifiedID("OpenAI", "gpt-4o"))
}

func TestProviderQualifiedID_Gemini(t *testing.T) {
	assert.Equal(t, "google/gemini-2.0-flash", ProviderQualifiedID("Gemini", "gemini-2.0-flash"))
}

func TestProviderQualifiedID_OpenRouter_PassThrough(t *testing.T) {
	// OpenRouter IDs already carry the provider prefix; pass them as-is.
	assert.Equal(t, "meta-llama/llama-3-70b", ProviderQualifiedID("OpenRouter", "meta-llama/llama-3-70b"))
}

func TestProviderQualifiedID_LiteLLM_PassThrough(t *testing.T) {
	assert.Equal(t, "litellm/claude-sonnet-4-6", ProviderQualifiedID("LiteLLM", "litellm/claude-sonnet-4-6"))
}

// ─── PricingFor ───────────────────────────────────────────────────────────────

func TestPricingFor_KnownModel(t *testing.T) {
	resetDynamicPricing(t)
	in, out, ok := PricingFor("claude-sonnet-4-6")
	assert.True(t, ok)
	assert.Greater(t, in, 0.0)
	assert.Greater(t, out, 0.0)
}

func TestPricingFor_QualifiedIDFallback(t *testing.T) {
	resetDynamicPricing(t)
	in1, out1, ok1 := PricingFor("claude-sonnet-4-6")
	in2, out2, ok2 := PricingFor("anthropic/claude-sonnet-4-6")
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, in1, in2)
	assert.Equal(t, out1, out2)
}

func TestPricingFor_UnknownModel_ReturnsFalse(t *testing.T) {
	resetDynamicPricing(t)
	_, _, ok := PricingFor("no-such-model-xyz")
	assert.False(t, ok)
}

// TestLoadPricing_MissingCache_FallsBackToHardcoded verifies that when no
// cache file exists and the OpenRouter fetch fails, the hardcoded table is used.
func TestLoadPricing_MissingCache_FallsBackToHardcoded(t *testing.T) {
	resetDynamicPricing(t)
	defer resetDynamicPricing(t)

	LoadPricing(filepath.Join(t.TempDir(), "nonexistent.json"))

	// claude-sonnet-4-6 is in the hardcoded table, so this must be non-zero.
	cost := CostUSD("claude-sonnet-4-6", 1_000_000, 0, 0, 0)
	assert.Greater(t, cost, 0.0, "hardcoded fallback must provide non-zero price")
}
