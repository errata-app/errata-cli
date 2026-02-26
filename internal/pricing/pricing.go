package pricing

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// modelPricing holds per-million-token prices and context window size for a model.
// InputPMT / OutputPMT = USD price per million tokens.
// CacheReadPMT = USD per million cache-read tokens (0 = unknown; falls back to InputPMT).
// CacheWritePMT = USD per million cache-creation tokens (0 = no charge or unknown).
// ContextWindow = max input context size in tokens; 0 means unknown.
type modelPricing struct {
	InputPMT      float64 `json:"input_pmt"`
	OutputPMT     float64 `json:"output_pmt"`
	CacheReadPMT  float64 `json:"cache_read_pmt,omitempty"`
	CacheWritePMT float64 `json:"cache_write_pmt,omitempty"`
	ContextWindow int64   `json:"context_window,omitempty"`
}

// pricingTable is the hardcoded last-resort fallback, keyed by bare model ID.
// Update this when providers change rates and the OpenRouter fetch is unavailable.
// Cache rates:
//   - Anthropic: read = 10% of InputPMT, write = 125% of InputPMT
//   - OpenAI:    read = 50% of InputPMT, no write charge
//   - Google:    read = 25% of InputPMT, no write charge
var pricingTable = map[string]modelPricing{
	// Anthropic — cache read 10%, cache write 125% of input rate
	"claude-opus-4-6":           {InputPMT: 15.00, OutputPMT: 75.00, CacheReadPMT: 1.50, CacheWritePMT: 18.75, ContextWindow: 200_000},
	"claude-sonnet-4-6":         {InputPMT: 3.00, OutputPMT: 15.00, CacheReadPMT: 0.30, CacheWritePMT: 3.75, ContextWindow: 200_000},
	"claude-haiku-4-5":          {InputPMT: 0.80, OutputPMT: 4.00, CacheReadPMT: 0.08, CacheWritePMT: 1.00, ContextWindow: 200_000},
	"claude-haiku-4-5-20251001": {InputPMT: 0.80, OutputPMT: 4.00, CacheReadPMT: 0.08, CacheWritePMT: 1.00, ContextWindow: 200_000},
	// OpenAI — cache read 50% of input rate, no write charge
	"gpt-4o":      {InputPMT: 2.50, OutputPMT: 10.00, CacheReadPMT: 1.25, ContextWindow: 128_000},
	"gpt-4o-mini": {InputPMT: 0.15, OutputPMT: 0.60, CacheReadPMT: 0.075, ContextWindow: 128_000},
	"o1":          {InputPMT: 15.00, OutputPMT: 60.00, CacheReadPMT: 7.50, ContextWindow: 200_000},
	"o3-mini":     {InputPMT: 1.10, OutputPMT: 4.40, CacheReadPMT: 0.55, ContextWindow: 200_000},
	// Google — cached content 25% of input rate, no write charge
	"gemini-2.0-flash": {InputPMT: 0.075, OutputPMT: 0.30, CacheReadPMT: 0.01875, ContextWindow: 1_000_000},
	"gemini-1.5-pro":   {InputPMT: 1.25, OutputPMT: 5.00, CacheReadPMT: 0.3125, ContextWindow: 2_000_000},
	"gemini-1.5-flash": {InputPMT: 0.075, OutputPMT: 0.30, CacheReadPMT: 0.01875, ContextWindow: 1_000_000},
}

var (
	pricingMu      sync.RWMutex
	dynamicPricing map[string]modelPricing // keyed by qualified "provider/model" OpenRouter IDs
)

// pricingCacheFile is the on-disk cache format.
type pricingCacheFile struct {
	FetchedAt time.Time              `json:"fetched_at"`
	Models    map[string]modelPricing `json:"models"`
}

// LoadPricing populates the dynamic pricing table at startup using OpenRouter's
// public model listing as the data source.
//
// Fallback chain:
//
//	fresh local cache (< 24 h) → OpenRouter fetch + update cache →
//	stale local cache → hardcoded pricingTable
//
// Blocking with a 5-second HTTP timeout. Safe to call before any goroutines start.
func LoadPricing(cacheFile string) {
	cached := readPricingCache(cacheFile)
	if cached != nil && time.Since(cached.FetchedAt) < 24*time.Hour {
		setDynamicPricing(cached.Models)
		return
	}

	fetched, err := fetchOpenRouterPricing()
	if err == nil {
		setDynamicPricing(fetched)
		writePricingCache(cacheFile, &pricingCacheFile{
			FetchedAt: time.Now(),
			Models:    fetched,
		})
		return
	}
	log.Printf("pricing: OpenRouter fetch failed: %v", err)

	if cached != nil {
		log.Printf("pricing: using stale cache (age %s)", time.Since(cached.FetchedAt).Round(time.Minute))
		setDynamicPricing(cached.Models)
		return
	}

	log.Printf("pricing: using hardcoded fallback table")
	// dynamicPricing stays nil; CostUSD falls back to pricingTable
}

func setDynamicPricing(m map[string]modelPricing) {
	pricingMu.Lock()
	dynamicPricing = m
	pricingMu.Unlock()
}

// CostUSD returns the estimated USD cost for a completed run.
//
// qualifiedID should be the OpenRouter-style "provider/model" key
// (e.g. "anthropic/claude-sonnet-4-6"). Native adapters pass their
// provider prefix; OpenRouter adapters pass the model ID as-is.
//
// Lookup order:
//  1. qualifiedID in dynamic pricing (OpenRouter data)
//  2. qualifiedID in hardcoded table
//  3. bare portion after the first "/" in both tables (for native-adapter fallback)
//
// Returns 0 for unknown models — the UI omits the cost field in that case.
// ProviderQualifiedID returns the OpenRouter-style "provider/model" key for a
// model ID from a given provider. For OpenRouter and LiteLLM the model ID
// already carries the required prefix and is returned as-is.
func ProviderQualifiedID(provider, modelID string) string {
	switch provider {
	case "Anthropic":
		return "anthropic/" + modelID
	case "OpenAI":
		return "openai/" + modelID
	case "Gemini":
		return "google/" + modelID
	case "Bedrock":
		return "bedrock/" + modelID
	case "AzureOpenAI":
		return "azure/" + modelID
	case "VertexAI":
		return "google/" + modelID // same models as Gemini
	default: // OpenRouter ("provider/model"), LiteLLM ("litellm/X")
		return modelID
	}
}

// dateSuffixRE matches a trailing date suffix in either of two formats:
//   - -20250714     (YYYYMMDD  — Anthropic, Google)
//   - -2024-08-06   (YYYY-MM-DD — OpenAI)
var dateSuffixRE = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2}|\d{8})$`)

// digitHyphenRE matches a hyphen between two single digits (e.g. "4-5" in
// "claude-opus-4-5"). Used to normalize Anthropic-style version hyphens
// ("claude-opus-4-5") to OpenRouter-style dots ("claude-opus-4.5").
var digitHyphenRE = regexp.MustCompile(`(\d)-(\d)`)

// stripDateSuffix removes a trailing date suffix from a model ID, if present.
// Returns the original string unchanged when no date suffix is found.
func stripDateSuffix(id string) string {
	return dateSuffixRE.ReplaceAllString(id, "")
}

// hyphensToDots replaces hyphens between adjacent digits with dots.
// This normalizes Anthropic-style version hyphens ("claude-opus-4-5") to
// the OpenRouter-style dot notation ("claude-opus-4.5").
func hyphensToDots(id string) string {
	return digitHyphenRE.ReplaceAllString(id, "${1}.${2}")
}

// resolvePricing looks up pricing for qualifiedID using a six-step fallback:
//  1. Exact match on qualified ID (e.g. "anthropic/claude-sonnet-4-6-20250714")
//  2. Bare portion after "/" (e.g. "claude-sonnet-4-6-20250714")
//  3. Qualified ID with date suffix stripped (e.g. "anthropic/claude-sonnet-4-6")
//  4. Bare ID with date suffix stripped (e.g. "claude-sonnet-4-6")
//  5. Qualified ID with digit-hyphens replaced by dots (e.g. "anthropic/claude-opus-4.5")
//  6. Bare ID with digit-hyphens replaced by dots (e.g. "claude-opus-4.5")
func resolvePricing(qualifiedID string) (modelPricing, bool) {
	// 1. Exact match on qualified ID.
	if p, ok := lookupPricing(qualifiedID); ok {
		return p, true
	}

	// 2. Strip provider prefix, try bare ID.
	bare := qualifiedID
	if _, after, found := strings.Cut(qualifiedID, "/"); found {
		bare = after
		if p, ok := lookupPricing(bare); ok {
			return p, true
		}
	}

	// 3. Strip date suffix from qualified ID.
	strippedQualified := stripDateSuffix(qualifiedID)
	if strippedQualified != qualifiedID {
		if p, ok := lookupPricing(strippedQualified); ok {
			return p, true
		}
	}

	// 4. Strip date suffix from bare ID.
	strippedBare := stripDateSuffix(bare)
	if strippedBare != bare {
		if p, ok := lookupPricing(strippedBare); ok {
			return p, true
		}
	}

	// 5. Normalize digit-hyphens to dots on qualified ID (with date already stripped).
	// Covers the Anthropic "claude-opus-4-5" → OpenRouter "claude-opus-4.5" mismatch.
	dottedQualified := hyphensToDots(strippedQualified)
	if dottedQualified != strippedQualified {
		if p, ok := lookupPricing(dottedQualified); ok {
			return p, true
		}
	}

	// 6. Same normalization on bare ID.
	dottedBare := hyphensToDots(strippedBare)
	if dottedBare != strippedBare {
		if p, ok := lookupPricing(dottedBare); ok {
			return p, true
		}
	}

	return modelPricing{}, false
}

// CostUSD returns the estimated USD cost for a completed run.
//
// qualifiedID should be the OpenRouter-style "provider/model" key
// (e.g. "anthropic/claude-sonnet-4-6"). Native adapters pass their
// provider prefix; OpenRouter adapters pass the model ID as-is.
//
// regularInput = non-cached input tokens.
// cacheRead    = tokens served from cache at a discounted rate.
// cacheCreation = tokens written to cache at a premium rate (Anthropic only).
// output       = output tokens.
//
// When CacheReadPMT is 0 (model not in hardcoded table or not yet fetched),
// all input tokens are charged at InputPMT as a conservative fallback.
// Returns 0 for unknown models — the UI omits the cost field in that case.
func CostUSD(qualifiedID string, regularInput, cacheRead, cacheCreation, output int64) float64 {
	p, ok := resolvePricing(qualifiedID)
	if !ok {
		return 0
	}
	if p.CacheReadPMT == 0 {
		// No cache rates known — charge all input at the standard rate.
		totalInput := regularInput + cacheRead + cacheCreation
		return (float64(totalInput)*p.InputPMT + float64(output)*p.OutputPMT) / 1_000_000
	}
	return (float64(regularInput)*p.InputPMT +
		float64(cacheRead)*p.CacheReadPMT +
		float64(cacheCreation)*p.CacheWritePMT +
		float64(output)*p.OutputPMT) / 1_000_000
}

func lookupPricing(key string) (modelPricing, bool) {
	pricingMu.RLock()
	dp := dynamicPricing
	pricingMu.RUnlock()

	if dp != nil {
		if p, ok := dp[key]; ok {
			return p, true
		}
	}
	p, ok := pricingTable[key]
	return p, ok
}

// roundPMT rounds a per-million-token price to 6 decimal places, which is
// enough to eliminate IEEE 754 noise (e.g. 0.7999999999999999 → 0.8) while
// preserving meaningful sub-cent precision.
func roundPMT(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// ─── OpenRouter fetch ─────────────────────────────────────────────────────────

const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

type orModelsResp struct {
	Data []struct {
		ID      string `json:"id"`
		Pricing struct {
			Prompt          string `json:"prompt"`
			Completion      string `json:"completion"`
			InputCacheRead  string `json:"input_cache_read"`
			InputCacheWrite string `json:"input_cache_write"`
		} `json:"pricing"`
		ContextLength int64 `json:"context_length"`
	} `json:"data"`
}

func fetchOpenRouterPricing() (map[string]modelPricing, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(openRouterModelsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter /models returned HTTP %d", resp.StatusCode)
	}

	var parsed orModelsResp
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	table := make(map[string]modelPricing, len(parsed.Data))
	for _, m := range parsed.Data {
		inp, err1 := strconv.ParseFloat(m.Pricing.Prompt, 64)
		out, err2 := strconv.ParseFloat(m.Pricing.Completion, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if inp == 0 && out == 0 {
			continue // skip free / unknown-price models
		}
		// OpenRouter prices are per-token; convert to per-million-token.
		// Round to 6 decimal places to eliminate IEEE 754 floating-point noise
		// (e.g. 0.0000008 * 1e6 = 0.7999999999999999 → 0.8).
		p := modelPricing{
			InputPMT:      roundPMT(inp * 1_000_000),
			OutputPMT:     roundPMT(out * 1_000_000),
			ContextWindow: m.ContextLength,
		}
		// Cache rates are optional — only populated when the provider exposes them.
		if cr, err := strconv.ParseFloat(m.Pricing.InputCacheRead, 64); err == nil && cr > 0 {
			p.CacheReadPMT = roundPMT(cr * 1_000_000)
		}
		if cw, err := strconv.ParseFloat(m.Pricing.InputCacheWrite, 64); err == nil && cw > 0 {
			p.CacheWritePMT = roundPMT(cw * 1_000_000)
		}
		table[m.ID] = p
	}
	return table, nil
}

// PricingFor returns the per-million-token USD rates for a model. qualifiedID
// follows the same "provider/model" convention as CostUSD (e.g.
// "anthropic/claude-sonnet-4-6"). Returns ok=false for unknown models.
func PricingFor(qualifiedID string) (inputPMT, outputPMT float64, ok bool) {
	p, found := resolvePricing(qualifiedID)
	if !found {
		return 0, 0, false
	}
	return p.InputPMT, p.OutputPMT, true
}

// ContextWindowTokens returns the known context window size in tokens for modelID, or 0
// if the model is unknown. Uses the same qualified→bare lookup chain as CostUSD.
func ContextWindowTokens(modelID string) int64 {
	p, ok := resolvePricing(modelID)
	if ok && p.ContextWindow > 0 {
		return p.ContextWindow
	}
	return 0
}

// ─── Cache I/O ────────────────────────────────────────────────────────────────

func readPricingCache(path string) *pricingCacheFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c pricingCacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	if len(c.Models) == 0 {
		return nil
	}
	// Validate that at least one entry has non-zero prices. A cache where all
	// entries are {0,0} indicates a corrupt write (e.g. from the unexported-fields
	// bug) and should be treated as missing so a fresh fetch is triggered.
	for _, p := range c.Models {
		if p.InputPMT > 0 || p.OutputPMT > 0 {
			return &c
		}
	}
	return nil
}

func writePricingCache(path string, c *pricingCacheFile) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}
