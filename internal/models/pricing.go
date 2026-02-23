package models

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// modelPricing holds per-million-token prices for a model.
// InputPMT / OutputPMT = USD price per million tokens.
type modelPricing struct {
	InputPMT  float64 `json:"input_pmt"`
	OutputPMT float64 `json:"output_pmt"`
}

// pricingTable is the hardcoded last-resort fallback, keyed by bare model ID.
// Update this when providers change rates and the OpenRouter fetch is unavailable.
var pricingTable = map[string]modelPricing{
	// Anthropic
	"claude-opus-4-6":           {15.00, 75.00},
	"claude-sonnet-4-6":         {3.00, 15.00},
	"claude-haiku-4-5":          {0.80, 4.00},
	"claude-haiku-4-5-20251001": {0.80, 4.00},
	// OpenAI
	"gpt-4o":      {2.50, 10.00},
	"gpt-4o-mini": {0.15, 0.60},
	"o1":          {15.00, 60.00},
	"o3-mini":     {1.10, 4.40},
	// Google
	"gemini-2.0-flash": {0.075, 0.30},
	"gemini-1.5-pro":   {1.25, 5.00},
	"gemini-1.5-flash": {0.075, 0.30},
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
func CostUSD(qualifiedID string, inputTokens, outputTokens int64) float64 {
	p, ok := lookupPricing(qualifiedID)
	if !ok && strings.Contains(qualifiedID, "/") {
		bare := qualifiedID[strings.Index(qualifiedID, "/")+1:]
		p, ok = lookupPricing(bare)
	}
	if !ok {
		return 0
	}
	return (float64(inputTokens)*p.InputPMT + float64(outputTokens)*p.OutputPMT) / 1_000_000
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

// ─── OpenRouter fetch ─────────────────────────────────────────────────────────

const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

type orModelsResp struct {
	Data []struct {
		ID      string `json:"id"`
		Pricing struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
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
		table[m.ID] = modelPricing{
			InputPMT:  inp * 1_000_000,
			OutputPMT: out * 1_000_000,
		}
	}
	return table, nil
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
