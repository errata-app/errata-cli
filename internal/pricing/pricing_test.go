package pricing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	cost := CostUSD("claude-sonnet-4-6", 1_000_000, 1_000_000)
	assert.InDelta(t, 18.0, cost, 0.001) // 3.00 + 15.00
}

func TestCostUSD_UnknownModel_ReturnsZero(t *testing.T) {
	resetDynamicPricing(t)
	assert.Zero(t, CostUSD("no-such-model-xyz", 1_000_000, 1_000_000))
}

// TestCostUSD_QualifiedIDFallback verifies that "provider/model" strips the
// prefix and falls back to the bare model ID in the hardcoded table.
func TestCostUSD_QualifiedIDFallback(t *testing.T) {
	resetDynamicPricing(t)
	// "anthropic/claude-sonnet-4-6" is not in the hardcoded table, but "claude-sonnet-4-6" is.
	cost := CostUSD("anthropic/claude-sonnet-4-6", 1_000_000, 1_000_000)
	assert.InDelta(t, 18.0, cost, 0.001)
}

func TestCostUSD_ZeroTokens(t *testing.T) {
	resetDynamicPricing(t)
	assert.Zero(t, CostUSD("claude-sonnet-4-6", 0, 0))
}

func TestCostUSD_OnlyInputTokens(t *testing.T) {
	resetDynamicPricing(t)
	// claude-sonnet-4-6: $3.00/M input
	cost := CostUSD("claude-sonnet-4-6", 1_000_000, 0)
	assert.InDelta(t, 3.0, cost, 0.001)
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
	cost := CostUSD("pricing-roundtrip-sentinel", 1_000_000, 1_000_000)
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
	assert.InDelta(t, in1, in2, 0.0001)
	assert.InDelta(t, out1, out2, 0.0001)
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
	cost := CostUSD("claude-sonnet-4-6", 1_000_000, 0)
	assert.Greater(t, cost, 0.0, "hardcoded fallback must provide non-zero price")
}

// ─── stripDateSuffix ────────────────────────────────────────────────────────

func TestStripDateSuffix(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"YYYYMMDD suffix", "claude-sonnet-4-6-20250714", "claude-sonnet-4-6"},
		{"YYYY-MM-DD suffix", "gpt-4o-2024-08-06", "gpt-4o"},
		{"no suffix", "claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"no suffix short", "o1", "o1"},
		{"no suffix gpt-4o-mini", "gpt-4o-mini", "gpt-4o-mini"},
		{"no suffix o3-mini", "o3-mini", "o3-mini"},
		{"no suffix gemini version", "gemini-2.0-flash", "gemini-2.0-flash"},
		{"qualified YYYYMMDD", "anthropic/claude-sonnet-4-6-20250714", "anthropic/claude-sonnet-4-6"},
		{"qualified YYYY-MM-DD", "openai/gpt-4o-2024-08-06", "openai/gpt-4o"},
		{"qualified no suffix", "anthropic/claude-sonnet-4-6", "anthropic/claude-sonnet-4-6"},
		{"empty string", "", ""},
		{"trailing 7 digits not stripped", "model-1234567", "model-1234567"},
		{"trailing 9 digits not stripped", "model-123456789", "model-123456789"},
		{"mini with date", "gpt-4o-mini-2024-07-18", "gpt-4o-mini"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, stripDateSuffix(tt.input))
		})
	}
}

// ─── Date-suffix fallback: CostUSD ─────────────────────────────────────────

// TestCostUSD_DateSuffix_YYYYMMDD verifies that a date-suffixed Anthropic model
// falls back to the base model's pricing in the hardcoded table.
func TestCostUSD_DateSuffix_YYYYMMDD(t *testing.T) {
	resetDynamicPricing(t)
	cost := CostUSD("anthropic/claude-sonnet-4-6-20250714", 1_000_000, 1_000_000)
	assert.InDelta(t, 18.0, cost, 0.001) // $3 in + $15 out
}

// TestCostUSD_DateSuffix_ISO verifies the OpenAI YYYY-MM-DD date format.
func TestCostUSD_DateSuffix_ISO(t *testing.T) {
	resetDynamicPricing(t)
	cost := CostUSD("openai/gpt-4o-2024-08-06", 1_000_000, 1_000_000)
	assert.InDelta(t, 12.50, cost, 0.001) // $2.50 in + $10 out
}

// TestCostUSD_DateSuffix_BareID verifies fallback works without a provider prefix.
func TestCostUSD_DateSuffix_BareID(t *testing.T) {
	resetDynamicPricing(t)
	cost := CostUSD("claude-sonnet-4-6-20250714", 1_000_000, 1_000_000)
	assert.InDelta(t, 18.0, cost, 0.001)
}

// TestCostUSD_DateSuffix_ExactMatchTakesPrecedence verifies that a date-suffixed
// model with its own explicit pricing entry uses that entry, not the stripped base.
func TestCostUSD_DateSuffix_ExactMatchTakesPrecedence(t *testing.T) {
	resetDynamicPricing(t)
	// "claude-haiku-4-5-20251001" has its own entry in the hardcoded table.
	cost := CostUSD("claude-haiku-4-5-20251001", 1_000_000, 1_000_000)
	expected := 0.80 + 4.00 // InputPMT + OutputPMT for 1M each
	assert.InDelta(t, expected, cost, 0.001)
}

// TestCostUSD_DateSuffix_DynamicExactMatchTakesPrecedence verifies that a
// date-suffixed model with dynamic pricing uses the dynamic entry, not the
// stripped fallback.
func TestCostUSD_DateSuffix_DynamicExactMatchTakesPrecedence(t *testing.T) {
	resetDynamicPricing(t)
	pricingMu.Lock()
	dynamicPricing = map[string]modelPricing{
		"openai/gpt-4o-2024-08-06": {InputPMT: 99.0, OutputPMT: 99.0},
	}
	pricingMu.Unlock()
	defer resetDynamicPricing(t)

	cost := CostUSD("openai/gpt-4o-2024-08-06", 1_000_000, 1_000_000)
	assert.InDelta(t, 198.0, cost, 0.001, "dynamic exact match should take precedence")
}

// TestCostUSD_DateSuffix_NoBaseMatch verifies that stripping still returns 0
// when the base model is also unknown.
func TestCostUSD_DateSuffix_NoBaseMatch(t *testing.T) {
	resetDynamicPricing(t)
	cost := CostUSD("unknown/mystery-model-20250714", 1_000_000, 1_000_000)
	assert.Zero(t, cost)
}

// TestCostUSD_NonDateSuffix_NotStripped ensures that non-date suffixes like
// "mini" are not accidentally stripped.
func TestCostUSD_NonDateSuffix_NotStripped(t *testing.T) {
	resetDynamicPricing(t)
	cost := CostUSD("gpt-4o-mini", 1_000_000, 1_000_000)
	assert.InDelta(t, 0.75, cost, 0.001) // $0.15 in + $0.60 out
}

// ─── Date-suffix fallback: PricingFor ───────────────────────────────────────

func TestPricingFor_DateSuffix_YYYYMMDD(t *testing.T) {
	resetDynamicPricing(t)
	in, out, ok := PricingFor("anthropic/claude-sonnet-4-6-20250714")
	assert.True(t, ok)
	assert.InDelta(t, 3.0, in, 0.001)
	assert.InDelta(t, 15.0, out, 0.001)
}

func TestPricingFor_DateSuffix_ISO(t *testing.T) {
	resetDynamicPricing(t)
	in, out, ok := PricingFor("openai/gpt-4o-2024-08-06")
	assert.True(t, ok)
	assert.InDelta(t, 2.50, in, 0.001)
	assert.InDelta(t, 10.0, out, 0.001)
}

// ─── Date-suffix fallback: ContextWindowTokens ──────────────────────────────

func TestContextWindowTokens_DateSuffix_YYYYMMDD(t *testing.T) {
	resetDynamicPricing(t)
	cw := ContextWindowTokens("anthropic/claude-sonnet-4-6-20250714")
	assert.Equal(t, int64(200_000), cw)
}

func TestContextWindowTokens_DateSuffix_ISO(t *testing.T) {
	resetDynamicPricing(t)
	cw := ContextWindowTokens("openai/gpt-4o-2024-08-06")
	assert.Equal(t, int64(128_000), cw)
}

// ─── Digit-hyphen-to-dot normalization ──────────────────────────────────────

func TestHyphensToDots(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"version hyphen", "claude-opus-4-5", "claude-opus-4.5"},
		{"qualified version hyphen", "anthropic/claude-opus-4-5", "anthropic/claude-opus-4.5"},
		{"already dotted", "claude-opus-4.5", "claude-opus-4.5"},
		{"no version digits", "gpt-4o-mini", "gpt-4o-mini"},
		{"multi-digit version", "gemini-2.0-flash", "gemini-2.0-flash"},
		{"no change needed", "o1", "o1"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, hyphensToDots(tt.input))
		})
	}
}

// TestResolvePricing_DotNormalization verifies that Anthropic-style model IDs
// with version hyphens (claude-opus-4-5) resolve against OpenRouter-style
// dot-separated entries (anthropic/claude-opus-4.5).
func TestResolvePricing_DotNormalization(t *testing.T) {
	resetDynamicPricing(t)
	pricingMu.Lock()
	dynamicPricing = map[string]modelPricing{
		"anthropic/claude-opus-4.5": {InputPMT: 5.0, OutputPMT: 25.0, ContextWindow: 200_000},
	}
	pricingMu.Unlock()
	defer resetDynamicPricing(t)

	// Qualified ID with hyphen version + date suffix — should resolve via
	// date strip + dot normalization.
	cost := CostUSD("anthropic/claude-opus-4-5-20251101", 1_000_000, 1_000_000)
	assert.InDelta(t, 30.0, cost, 0.001, "should resolve via dot normalization") // $5 + $25

	// Bare ID with hyphen version (no date suffix).
	p, ok := resolvePricing("anthropic/claude-opus-4-5")
	assert.True(t, ok, "qualified ID with hyphen version should resolve")
	assert.InDelta(t, 5.0, p.InputPMT, 0.001)
}

// TestResolvePricing_DotNormalization_ContextWindow verifies that context
// window data is also accessible through the dot normalization path.
func TestResolvePricing_DotNormalization_ContextWindow(t *testing.T) {
	resetDynamicPricing(t)
	pricingMu.Lock()
	dynamicPricing = map[string]modelPricing{
		"anthropic/claude-opus-4.1": {InputPMT: 15.0, OutputPMT: 75.0, ContextWindow: 200_000},
	}
	pricingMu.Unlock()
	defer resetDynamicPricing(t)

	cw := ContextWindowTokens("anthropic/claude-opus-4-1-20250805")
	assert.Equal(t, int64(200_000), cw)
}

// ─── roundPMT ───────────────────────────────────────────────────────────────

func TestRoundPMT(t *testing.T) {
	tests := []struct {
		name   string
		input  float64
		expect float64
	}{
		{"clean value", 3.0, 3.0},
		{"fp noise 0.8", 0.7999999999999999, 0.8},
		{"fp noise 0.4", 0.39999999999999997, 0.4},
		{"fp noise 0.1", 0.09999999999999999, 0.1},
		{"fp noise 1.6", 1.5999999999999999, 1.6},
		{"sub-cent preserved", 0.075, 0.075},
		{"small sub-cent", 0.01875, 0.01875},
		{"zero", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.expect, roundPMT(tt.input), 1e-9)
		})
	}
}

// ─── readPricingCache edge cases ──────────────────────────────────────────────

func TestReadPricingCache_InvalidJSON_ReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))
	assert.Nil(t, readPricingCache(path))
}

func TestReadPricingCache_EmptyModels_ReturnsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	raw := `{"fetched_at":"2099-01-01T00:00:00Z","models":{}}`
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))
	assert.Nil(t, readPricingCache(path))
}

// ─── writePricingCache ─────────────────────────────────────────────────────────

func TestWritePricingCache_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	now := time.Now().Truncate(time.Second)
	c := &pricingCacheFile{
		FetchedAt: now,
		Models: map[string]modelPricing{
			"test/model": {InputPMT: 5.0, OutputPMT: 15.0, ContextWindow: 100_000},
		},
	}
	writePricingCache(path, c)

	got := readPricingCache(path)
	require.NotNil(t, got)
	assert.InDelta(t, 5.0, got.Models["test/model"].InputPMT, 0.001)
	assert.Equal(t, int64(100_000), got.Models["test/model"].ContextWindow)
}

func TestWritePricingCache_InvalidPath(t *testing.T) {
	// Should not panic on invalid path.
	writePricingCache("/dev/null/invalid/path/cache.json", &pricingCacheFile{
		FetchedAt: time.Now(),
		Models:    map[string]modelPricing{"x": {InputPMT: 1}},
	})
}

// ─── fetchOpenRouterPricing with httptest ──────────────────────────────────────

func TestFetchOpenRouterPricing_ParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "test/model-a",
					"pricing": {"prompt": "0.000003", "completion": "0.000015"},
					"context_length": 200000
				},
				{
					"id": "test/model-b",
					"pricing": {"prompt": "0", "completion": "0"},
					"context_length": 100000
				},
				{
					"id": "test/model-c",
					"pricing": {"prompt": "0.000001", "completion": "0.000004", "input_cache_read": "0.0000001", "input_cache_write": "0.00000125"},
					"context_length": 128000
				}
			]
		}`))
	}))
	defer srv.Close()

	// Temporarily override the URL.
	origURL := openRouterModelsURL
	// We can't reassign a const, so we use a test-local fetch with the httptest URL.
	// Instead, test the parsing by calling the server and decoding manually.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	var parsed orModelsResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&parsed))
	assert.Len(t, parsed.Data, 3)
	assert.Equal(t, "test/model-a", parsed.Data[0].ID)
	_ = origURL // acknowledge we can't override the const
}

// ─── LoadPricing: stale cache path ─────────────────────────────────────────────

// failTransport is an http.RoundTripper that always returns an error,
// used to force fetchOpenRouterPricing to fail without any network access.
type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("network disabled for test")
}

func TestLoadPricing_StaleCache_UsedWhenFetchFails(t *testing.T) {
	resetDynamicPricing(t)
	defer resetDynamicPricing(t)

	// Block all outbound HTTP so the OpenRouter fetch fails.
	orig := http.DefaultTransport
	http.DefaultTransport = failTransport{}
	defer func() { http.DefaultTransport = orig }()

	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "cache.json")

	// Write a stale cache (older than 24 hours).
	staleTime := time.Now().Add(-48 * time.Hour)
	c := pricingCacheFile{
		FetchedAt: staleTime,
		Models: map[string]modelPricing{
			"stale/test-model": {InputPMT: 42.0, OutputPMT: 84.0},
		},
	}
	data, err := json.Marshal(c)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cacheFile, data, 0o644))

	// LoadPricing: skip fresh cache → try fetch (fails) → use stale cache.
	LoadPricing(cacheFile)

	cost := CostUSD("stale/test-model", 1_000_000, 1_000_000)
	assert.InDelta(t, 126.0, cost, 0.001, "stale cache should provide pricing") // $42 + $84
}

func TestLoadPricing_NoCache_NoNetwork_FallsBackToHardcoded(t *testing.T) {
	resetDynamicPricing(t)
	defer resetDynamicPricing(t)

	orig := http.DefaultTransport
	http.DefaultTransport = failTransport{}
	defer func() { http.DefaultTransport = orig }()

	// No cache file at all.
	LoadPricing(filepath.Join(t.TempDir(), "nonexistent.json"))

	// Should fall back to hardcoded table.
	cost := CostUSD("claude-sonnet-4-6", 1_000_000, 0)
	assert.Greater(t, cost, 0.0, "hardcoded fallback must provide non-zero price")
}

// ─── resolvePricing: bare ID dot normalization ──────────────────────────────

func TestResolvePricing_BareID_DotNormalization(t *testing.T) {
	resetDynamicPricing(t)
	pricingMu.Lock()
	dynamicPricing = map[string]modelPricing{
		"claude-opus-4.6": {InputPMT: 15.0, OutputPMT: 75.0},
	}
	pricingMu.Unlock()
	defer resetDynamicPricing(t)

	// Bare ID "claude-opus-4-6" should resolve to "claude-opus-4.6" via dot normalization.
	p, ok := resolvePricing("claude-opus-4-6")
	assert.True(t, ok)
	assert.InDelta(t, 15.0, p.InputPMT, 0.001)
}
