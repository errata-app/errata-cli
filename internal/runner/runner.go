// Package runner fans out prompts to multiple model adapters concurrently.
package runner

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
)

const agentTimeout = 5 * time.Minute
const defaultMaxHistoryTurns = 20
const autoCompactThreshold = 0.80

func maxHistoryTurns() int {
	if v := os.Getenv("ERRATA_MAX_HISTORY_TURNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxHistoryTurns
}

// RunAll sends prompt to every adapter concurrently and returns all responses.
// histories is a per-adapter map (keyed by adapter ID) of prior conversation turns;
// nil or a missing key means a fresh conversation for that adapter.
// onEvent is called from goroutines — callers must be safe for concurrent use.
func RunAll(
	ctx      context.Context,
	adapters []models.ModelAdapter,
	histories map[string][]models.ConversationTurn,
	prompt   string,
	onEvent  func(modelID string, event models.AgentEvent),
	verbose  bool,
) []models.ModelResponse {
	results := make([]models.ModelResponse, len(adapters))
	var wg sync.WaitGroup

	for i, a := range adapters {
		i, a := i, a
		wg.Add(1)
		go func() {
			defer wg.Done()

			tctx, cancel := context.WithTimeout(ctx, agentTimeout)
			defer cancel()

			// filtered suppresses "text" and "error" events when not verbose.
			filtered := func(e models.AgentEvent) {
				if !verbose && (e.Type == "text" || e.Type == "error") {
					return
				}
				onEvent(a.ID(), e)
			}

			start := time.Now()
			h := TrimHistory(histories[a.ID()], maxHistoryTurns())
			resp, err := a.RunAgent(tctx, h, prompt, filtered)
			resp.ModelID = a.ID() // enforce: ModelID always matches the configured adapter ID

			if err != nil {
				filtered(models.AgentEvent{Type: "error", Data: err.Error()})
				results[i] = models.ModelResponse{
					ModelID:   a.ID(),
					LatencyMS: time.Since(start).Milliseconds(),
					Error:     err.Error(),
				}
				return
			}
			results[i] = resp
		}()
	}

	wg.Wait()
	return results
}

// AppendHistory updates histories with the results of a completed run.
// For each adapter (identified by adapterIDs[i]) whose response is successful
// and has non-empty text, it appends a user turn and an assistant turn.
// Error responses and write-only responses (empty text) are skipped.
// A nil histories map is initialized on first use. The updated map is returned.
func AppendHistory(
	histories  map[string][]models.ConversationTurn,
	adapterIDs []string,
	responses  []models.ModelResponse,
	prompt     string,
) map[string][]models.ConversationTurn {
	for i, resp := range responses {
		if i >= len(adapterIDs) {
			break
		}
		if !resp.OK() || resp.Text == "" {
			continue
		}
		if histories == nil {
			histories = make(map[string][]models.ConversationTurn)
		}
		id := adapterIDs[i]
		histories[id] = append(histories[id],
			models.ConversationTurn{Role: "user", Content: prompt},
			models.ConversationTurn{Role: "assistant", Content: resp.Text},
		)
	}
	return histories
}

// TrimHistory returns the most recent maxTurns turns from history, preserving complete
// user+assistant pairs. If len(history) <= maxTurns or maxTurns <= 0, history is returned
// unchanged. maxTurns is rounded down to the nearest even number.
func TrimHistory(history []models.ConversationTurn, maxTurns int) []models.ConversationTurn {
	if maxTurns <= 0 || len(history) <= maxTurns {
		return history
	}
	maxTurns = (maxTurns / 2) * 2 // keep whole pairs
	start := len(history) - maxTurns
	return history[start:]
}

// EstimateHistoryTokens returns a rough token count for history (4 chars ≈ 1 token).
func EstimateHistoryTokens(history []models.ConversationTurn) int64 {
	var n int
	for _, turn := range history {
		n += len(turn.Content)
	}
	return int64(n) / 4
}

// IsContextOverflowError reports whether errStr looks like a context-window-exceeded
// error from any supported provider.
func IsContextOverflowError(errStr string) bool {
	lower := strings.ToLower(errStr)
	for _, pat := range []string{
		"context_length_exceeded",
		"context window",
		"maximum context length",
		"prompt is too long",
		"prompt_too_long",
		"exceeds the model's maximum",
		"too many tokens",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// ShouldAutoCompact reports whether the history for adapterID has grown past the
// auto-compact threshold relative to the model's known context window.
// Returns false when the context window is unknown.
func ShouldAutoCompact(histories map[string][]models.ConversationTurn, adapterID string) bool {
	cw := pricing.ContextWindowTokens(adapterID)
	if cw == 0 {
		return false
	}
	est := EstimateHistoryTokens(histories[adapterID])
	return float64(est)/float64(cw) >= autoCompactThreshold
}

const compactPrompt = `Please write a complete but concise summary of our conversation so far.
Include all code discussed, decisions made, file paths involved, and any context needed to
continue seamlessly. Reply with ONLY the summary — no preamble.`

// CompactHistories calls each adapter to summarise its own conversation history.
// On success the full history is replaced with a single compact context pair.
// Adapters with no history, or whose compaction call fails, are left unchanged.
func CompactHistories(
	ctx      context.Context,
	adapters []models.ModelAdapter,
	histories map[string][]models.ConversationTurn,
	onEvent  func(modelID string, event models.AgentEvent),
) map[string][]models.ConversationTurn {
	for _, adapter := range adapters {
		h := histories[adapter.ID()]
		if len(h) == 0 {
			continue
		}
		resp, err := adapter.RunAgent(ctx, h, compactPrompt, func(e models.AgentEvent) {
			if onEvent != nil {
				onEvent(adapter.ID(), e)
			}
		})
		if err != nil || resp.Text == "" {
			continue
		}
		if histories == nil {
			histories = make(map[string][]models.ConversationTurn)
		}
		histories[adapter.ID()] = []models.ConversationTurn{
			{Role: "user", Content: "[Previous conversation — compacted]"},
			{Role: "assistant", Content: resp.Text},
		}
	}
	return histories
}
