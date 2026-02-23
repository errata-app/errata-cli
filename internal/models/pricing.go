package models

// modelPricing holds per-million-token prices for a model.
// inputPMT / outputPMT = USD price per million tokens.
type modelPricing struct{ inputPMT, outputPMT float64 }

// pricingTable maps model IDs to their public API pricing as of early 2026.
// Update manually when providers change rates.
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

// CostUSD returns the estimated USD cost for a completed run.
// Returns 0 for unknown model IDs (cost is silently omitted from UI).
func CostUSD(modelID string, inputTokens, outputTokens int64) float64 {
	p, ok := pricingTable[modelID]
	if !ok {
		return 0
	}
	return (float64(inputTokens)*p.inputPMT + float64(outputTokens)*p.outputPMT) / 1_000_000
}
