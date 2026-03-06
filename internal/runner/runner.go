// Package runner fans out prompts to multiple model adapters concurrently.
package runner

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/suarezc/errata/internal/checkpoint"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/prompt"
	"github.com/suarezc/errata/internal/tools"
)

// Last-resort fallbacks. In normal operation these are never reached because
// config.Load() applies the default recipe (pkg/recipe/default.recipe.md)
// which populates all fields before they reach RunOptions.
const agentTimeout = 5 * time.Minute
const defaultMaxHistoryTurns = 20
const autoCompactThreshold = 0.80

// ─── Run options (context-based) ─────────────────────────────────────────────

type runOptsKey struct{}

// RunOptions controls per-run behavior. Zero values fall back to package defaults.
type RunOptions struct {
	Timeout          time.Duration // 0 → agentTimeout (5 min)
	CompactThreshold float64       // 0 → autoCompactThreshold (0.80)
	MaxHistoryTurns  int           // 0 → defaultMaxHistoryTurns (20)
	MaxSteps         int           // 0 → unlimited agentic loop turns
	CheckpointPath   string        // "" disables incremental checkpointing
	WorkDirs         map[string]string // per-adapter working directory (adapter ID → dir path)
}

// WithRunOptions returns a context carrying the given RunOptions.
func WithRunOptions(ctx context.Context, opts RunOptions) context.Context {
	return context.WithValue(ctx, runOptsKey{}, opts)
}

// runOptsFromContext retrieves RunOptions from ctx, filling in package defaults
// for any zero values.
func runOptsFromContext(ctx context.Context) RunOptions {
	v, _ := ctx.Value(runOptsKey{}).(RunOptions)
	if v.Timeout == 0 {
		v.Timeout = agentTimeout
	}
	if v.CompactThreshold == 0 {
		v.CompactThreshold = autoCompactThreshold
	}
	if v.MaxHistoryTurns == 0 {
		v.MaxHistoryTurns = defaultMaxHistoryTurns
	}
	return v
}

// RunAll sends prompt to every adapter concurrently and returns all responses.
// histories is a per-adapter map (keyed by adapter ID) of prior conversation turns;
// nil or a missing key means a fresh conversation for that adapter.
// onEvent is called from goroutines — callers must be safe for concurrent use.
// onModelDone, if non-nil, is called from the adapter's goroutine as soon as it
// finishes (before RunAll returns). This lets callers render incremental completion
// (e.g. marking a TUI panel as "done") without waiting for the slowest adapter.
func RunAll(
	ctx      context.Context,
	adapters []models.ModelAdapter,
	histories map[string][]models.ConversationTurn,
	userPrompt string,
	onEvent  func(modelID string, event models.AgentEvent),
	onModelDone func(idx int, resp models.ModelResponse),
	verbose  bool,
) []models.ModelResponse {
	opts := runOptsFromContext(ctx)
	results := make([]models.ModelResponse, len(adapters))
	var wg sync.WaitGroup

	// Set up incremental checkpointing (survives SIGKILL/OOM/power loss).
	var saver *checkpoint.IncrementalSaver
	if opts.CheckpointPath != "" {
		adapterIDs := make([]string, len(adapters))
		for i, a := range adapters {
			adapterIDs[i] = a.ID()
		}
		saver = checkpoint.NewIncrementalSaver(opts.CheckpointPath, userPrompt, adapterIDs, verbose)
	}

	for i, a := range adapters {
		wg.Go(func() {
			tctx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
			if opts.MaxSteps > 0 {
				tctx = tools.WithMaxSteps(tctx, opts.MaxSteps)
			}
			if dir := opts.WorkDirs[a.ID()]; dir != "" {
				tctx = tools.WithWorkDir(tctx, dir)
				tctx = tools.WithDirectWrites(tctx, true)
			}

			// filtered suppresses "text" and "error" events when not verbose,
			// and intercepts "snapshot" events for incremental checkpointing.
			filtered := func(e models.AgentEvent) {
				if e.Type == models.EventSnapshot {
					if saver != nil {
						var ps models.PartialSnapshot
						if json.Unmarshal([]byte(e.Data), &ps) == nil {
							saver.Update(a.ID(), checkpoint.SnapshotFromPartial(a.ID(), ps))
						}
					}
					return // never forward snapshot events to UI
				}
				if e.Type == models.EventRequest {
					return // never forward request events to UI (captured by logging wrapper)
				}
				if !verbose && (e.Type == models.EventText || e.Type == models.EventError) {
					return
				}
				onEvent(a.ID(), e)
			}

			start := time.Now()
			h := TrimHistory(histories[a.ID()], opts.MaxHistoryTurns)
			resp, err := a.RunAgent(tctx, h, userPrompt, filtered)
			resp.ModelID = a.ID() // enforce: ModelID always matches the configured adapter ID

			if err != nil {
				resp.ModelID = a.ID()
				if resp.LatencyMS == 0 {
					resp.LatencyMS = time.Since(start).Milliseconds()
				}
				if resp.Error == "" {
					resp.Error = err.Error()
				}
				if IsContextOverflowError(resp.Error) {
					resp.StopReason = models.StopReasonContextOverflow
				}
				if !resp.Interrupted {
					filtered(models.AgentEvent{Type: models.EventError, Data: err.Error()})
				}
				results[i] = resp
				if saver != nil {
					saver.MarkCompleted(a.ID(), checkpoint.FromModelResponse(resp))
				}
				if onModelDone != nil {
					onModelDone(i, resp)
				}
				return
			}
			results[i] = resp
			if saver != nil {
				saver.MarkCompleted(a.ID(), checkpoint.FromModelResponse(resp))
			}
			if onModelDone != nil {
				onModelDone(i, resp)
			}
		})
	}

	wg.Wait()

	// Clean up checkpoint if all adapters completed without interruption.
	if saver != nil && !HasInterrupted(results) {
		if err := checkpoint.Clear(opts.CheckpointPath); err != nil {
			log.Printf("warning: failed to clear checkpoint: %v", err)
		}
	}

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
	userPrompt string,
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
			models.ConversationTurn{Role: "user", Content: userPrompt},
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
		"model_context_window_exceeded", // Bedrock Converse stop reason
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// ShouldAutoCompact reports whether the history for adapterID has grown past threshold
// relative to the model's known context window. threshold=0 uses the package default (0.80).
// Returns false when the context window is unknown.
func ShouldAutoCompact(histories map[string][]models.ConversationTurn, adapterID string, threshold float64) bool {
	cw := pricing.ContextWindowTokens(adapterID)
	if cw == 0 {
		return false
	}
	if threshold <= 0 {
		threshold = autoCompactThreshold
	}
	est := EstimateHistoryTokens(histories[adapterID])
	return float64(est)/float64(cw) >= threshold
}

// CompactHistories calls each adapter to summarise its own conversation history.
// On success the full history is replaced with a single compact context pair.
// Adapters with no history, or whose compaction call fails, are left unchanged.
//
// The summarization prompt is resolved per-model from the context (via
// prompt.ResolveSummarizationPrompt). If no payload is configured, it falls
// back to DefaultSummarizationPrompt.
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
		sumPrompt := prompt.ResolveSummarizationPrompt(ctx)
		resp, err := adapter.RunAgent(ctx, h, sumPrompt, func(e models.AgentEvent) {
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

// HasInterrupted reports whether any response in the slice has Interrupted set.
func HasInterrupted(responses []models.ModelResponse) bool {
	for _, r := range responses {
		if r.Interrupted {
			return true
		}
	}
	return false
}
