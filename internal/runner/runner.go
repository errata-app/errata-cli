// Package runner fans out prompts to multiple model adapters concurrently.
package runner

import (
	"context"
	"sync"
	"time"

	"github.com/suarezc/errata/internal/models"
)

const agentTimeout = 5 * time.Minute

// RunAll sends prompt to every adapter concurrently and returns all responses.
// onEvent is called from goroutines — callers must be safe for concurrent use.
func RunAll(
	ctx context.Context,
	adapters []models.ModelAdapter,
	prompt string,
	onEvent func(modelID string, event models.AgentEvent),
	verbose bool,
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

			start := time.Now()
			resp, err := a.RunAgent(tctx, prompt, func(e models.AgentEvent) {
				onEvent(a.ID(), e)
			}, verbose)

			if err != nil {
				onEvent(a.ID(), models.AgentEvent{Type: "error", Data: err.Error()})
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
