// Package runner fans out prompts to multiple model adapters concurrently.
package runner

import (
	"context"
	"sync"
	"time"

	"github.com/suarezc/errata/internal/models"
)

const agentTimeout = 5 * time.Minute

// Silly little comment to test prs
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
			resp, err := a.RunAgent(tctx, histories[a.ID()], prompt, filtered)

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
