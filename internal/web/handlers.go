package web

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/diff"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/tools"
)

// ─── WebSocket message types ──────────────────────────────────────────────────

// wsClientMsg is a message sent from the browser to the server.
type wsClientMsg struct {
	Type     string   `json:"type"`              // "run" | "select" | "cancel" | "set_models"
	Prompt   string   `json:"prompt,omitempty"`
	ModelID  string   `json:"model_id,omitempty"`
	ModelIDs []string `json:"model_ids,omitempty"`
	Verbose  bool     `json:"verbose,omitempty"`
}

// wsServerMsg is a message sent from the server to the browser.
type wsServerMsg struct {
	Type      string         `json:"type"`
	ModelID   string         `json:"model_id,omitempty"`
	EventType string         `json:"event_type,omitempty"`
	Data      string         `json:"data,omitempty"`
	Responses []responseData `json:"responses,omitempty"`
	Applied   []string       `json:"applied,omitempty"`
	Message   string         `json:"message,omitempty"`
	Models    []string       `json:"models,omitempty"`
}

// ─── Diff / response serialisation types (unchanged from SSE version) ─────────

type diffLineData struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

type fileDiffData struct {
	Path      string         `json:"path"`
	IsNew     bool           `json:"is_new"`
	Adds      int            `json:"adds"`
	Removes   int            `json:"removes"`
	Lines     []diffLineData `json:"lines"`
	Truncated int            `json:"truncated"`
}

type writeData struct {
	Path    string       `json:"path"`
	Content string       `json:"content"`
	Diff    fileDiffData `json:"diff"`
}

type responseData struct {
	ModelID             string      `json:"model_id"`
	Text                string      `json:"text"`
	LatencyMS           int64       `json:"latency_ms"`
	InputTokens         int64       `json:"input_tokens"`
	OutputTokens        int64       `json:"output_tokens"`
	CostUSD             float64     `json:"cost_usd"`
	ContextWindowTokens int64       `json:"context_window_tokens"`
	Error               string      `json:"error,omitempty"`
	ProposedWrites      []writeData `json:"proposed_writes"`
}

// ─── WebSocket handler ────────────────────────────────────────────────────────

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()

	// writeCh is the only path to conn. Never closed explicitly — garbage collected
	// after the handler returns. send() exits via ctx.Done() if the connection drops.
	writeCh := make(chan wsServerMsg, 64)

	// Write pump: sole goroutine that writes to conn.
	go func() {
		for {
			select {
			case <-ctx.Done():
				conn.Close(websocket.StatusNormalClosure, "")
				return
			case msg := <-writeCh:
				if err := wsjson.Write(ctx, conn, msg); err != nil {
					return
				}
			}
		}
	}()

	send := func(msg wsServerMsg) {
		select {
		case writeCh <- msg:
		case <-ctx.Done():
		}
	}

	var (
		mu             sync.Mutex
		cancelRun      context.CancelFunc     // cancel for the most recent run; nil initially
		lastRun        []models.ModelResponse  // results of last completed run
		lastPrompt     string
		activeAdapters []models.ModelAdapter   // nil = use all s.adapters
	)

	for {
		var msg wsClientMsg
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			break
		}

		switch msg.Type {

		case "run":
			if strings.TrimSpace(msg.Prompt) == "" {
				send(wsServerMsg{Type: "error", Message: "prompt required"})
				continue
			}

			// Cancel the previous run (if any) and start a new one.
			toRun := activeAdapters
			if toRun == nil {
				toRun = s.adapters
			}

			// Snapshot per-connection run state.
			mu.Lock()
			oldCancel := cancelRun
			runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			cancelRun = cancel
			mu.Unlock()

			// Snapshot server-level histories for the goroutine (read-only during run).
			s.histMu.RLock()
			histSnapshot := make(map[string][]models.ConversationTurn, len(s.histories))
			for k, v := range s.histories {
				histSnapshot[k] = v
			}
			s.histMu.RUnlock()

			if oldCancel != nil {
				oldCancel()
			}

			go func(prompt string, runCtx context.Context, cancel context.CancelFunc, verbose bool, adapters []models.ModelAdapter, hists map[string][]models.ConversationTurn) {
				defer cancel()

				effectiveHistories := hists
				for _, ad := range adapters {
					if runner.ShouldAutoCompact(effectiveHistories, ad.ID()) {
						send(wsServerMsg{Type: "agent_event", ModelID: ad.ID(), EventType: "text", Data: "[auto-compacting history…]"})
						effectiveHistories = runner.CompactHistories(
							runCtx, []models.ModelAdapter{ad},
							effectiveHistories, func(modelID string, e models.AgentEvent) {
								send(wsServerMsg{Type: "agent_event", ModelID: modelID, EventType: e.Type, Data: e.Data})
							},
						)
					}
				}

				rs := runner.RunAll(
					runCtx,
					adapters,
					effectiveHistories,
					prompt,
					func(modelID string, e models.AgentEvent) {
						send(wsServerMsg{
							Type:      "agent_event",
							ModelID:   modelID,
							EventType: e.Type,
							Data:      e.Data,
						})
					},
					verbose,
				)

				// If the context was cancelled (user cancel or new run started), report
				// and exit without overwriting lastRun.
				if runCtx.Err() != nil {
					send(wsServerMsg{Type: "cancelled"})
					return
				}

				adapterIDs := make([]string, len(adapters))
				for i, ad := range adapters {
					adapterIDs[i] = ad.ID()
				}
				mu.Lock()
				lastRun = rs
				lastPrompt = prompt
				mu.Unlock()

				s.histMu.Lock()
				s.histories = runner.AppendHistory(effectiveHistories, adapterIDs, rs, prompt)
				if err := history.Save(s.histPath, s.histories); err != nil {
					log.Printf("web: could not save history: %v", err)
				}
				s.histMu.Unlock()

				send(wsServerMsg{Type: "complete", Responses: buildCompletePayload(rs)})
			}(msg.Prompt, runCtx, cancel, msg.Verbose, toRun, histSnapshot)

		case "select":
			mu.Lock()
			rs := lastRun
			prompt := lastPrompt
			mu.Unlock()

			if rs == nil {
				send(wsServerMsg{Type: "error", Message: "no completed run to select from"})
				continue
			}

			var selected *models.ModelResponse
			for i := range rs {
				if rs[i].ModelID == msg.ModelID {
					selected = &rs[i]
					break
				}
			}
			if selected == nil {
				send(wsServerMsg{Type: "error", Message: "model not found"})
				continue
			}

			if err := tools.ApplyWrites(selected.ProposedWrites); err != nil {
				send(wsServerMsg{Type: "error", Message: err.Error()})
				continue
			}

			if err := preferences.Record(s.prefPath, prompt, msg.ModelID, s.sessionID, rs); err != nil {
				log.Printf("web: could not record preference: %v", err)
			}

			// Clear lastRun so the user can't double-select.
			mu.Lock()
			lastRun = nil
			mu.Unlock()

			applied := make([]string, 0, len(selected.ProposedWrites))
			for _, fw := range selected.ProposedWrites {
				applied = append(applied, fw.Path)
			}
			send(wsServerMsg{Type: "applied", Applied: applied})

		case "set_models":
			if len(msg.ModelIDs) == 0 {
				// Reset to all models.
				activeAdapters = nil
				ids := make([]string, len(s.adapters))
				for i, a := range s.adapters {
					ids[i] = a.ID()
				}
				send(wsServerMsg{Type: "models_set", Models: ids})
				continue
			}
			var selected []models.ModelAdapter
			for _, id := range msg.ModelIDs {
				var found models.ModelAdapter
				for _, a := range s.adapters {
					if a.ID() == id {
						found = a
						break
					}
				}
				if found == nil {
					available := make([]string, len(s.adapters))
					for i, a := range s.adapters {
						available[i] = a.ID()
					}
					send(wsServerMsg{
						Type:    "error",
						Message: "unknown model: " + id + ". Available: " + strings.Join(available, ", "),
					})
					continue
				}
				selected = append(selected, found)
			}
			activeAdapters = selected
			ids := make([]string, len(selected))
			for i, a := range selected {
				ids[i] = a.ID()
			}
			send(wsServerMsg{Type: "models_set", Models: ids})

		case "cancel":
			mu.Lock()
			cancel := cancelRun
			mu.Unlock()
			if cancel != nil {
				cancel()
			}

		case "compact":
			mu.Lock()
			toCompact := activeAdapters
			if toCompact == nil {
				toCompact = s.adapters
			}
			mu.Unlock()

			s.histMu.RLock()
			histsToCompact := make(map[string][]models.ConversationTurn, len(s.histories))
			for k, v := range s.histories {
				histsToCompact[k] = v
			}
			s.histMu.RUnlock()

			go func() {
				updated := runner.CompactHistories(
					ctx, toCompact, histsToCompact,
					func(modelID string, e models.AgentEvent) {
						send(wsServerMsg{Type: "agent_event", ModelID: modelID, EventType: e.Type, Data: e.Data})
					},
				)
				s.histMu.Lock()
				s.histories = updated
				if err := history.Save(s.histPath, s.histories); err != nil {
					log.Printf("web: could not save history: %v", err)
				}
				s.histMu.Unlock()
				send(wsServerMsg{Type: "compact_complete"})
			}()

		case "clear_history":
			s.histMu.Lock()
			s.histories = nil
			if err := history.Clear(s.histPath); err != nil {
				log.Printf("web: could not clear history: %v", err)
			}
			s.histMu.Unlock()
			send(wsServerMsg{Type: "history_cleared"})
		}
	}
}

// ─── REST handlers ────────────────────────────────────────────────────────────

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	tally := preferences.Summarize(s.prefPath)
	if tally == nil {
		tally = map[string]int{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tally": tally})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, len(s.adapters))
	for i, a := range s.adapters {
		ids[i] = a.ID()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"models": ids})
}

// ─── Available models handler ─────────────────────────────────────────────────

// providerModelsResult is the per-provider payload returned by /api/available-models.
// TotalCount is the raw API count before any chat filter; Count is the filtered count.
// When TotalCount > Count the provider filtered non-chat models (OpenAI, Gemini).
type providerModelsResult struct {
	Name       string   `json:"name"`
	Models     []string `json:"models"`
	Count      int      `json:"count"`
	TotalCount int      `json:"total_count"`
	Error      string   `json:"error,omitempty"`
}

func (s *Server) handleAvailableModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	active := make([]string, len(s.adapters))
	for i, a := range s.adapters {
		active[i] = a.ID()
	}

	raw := adapters.ListAvailableModels(ctx, s.cfg)
	providers := make([]providerModelsResult, len(raw))
	for i, p := range raw {
		entry := providerModelsResult{
			Name:       p.Provider,
			Models:     p.Models,
			Count:      len(p.Models),
			TotalCount: p.TotalCount,
		}
		if p.Err != nil {
			entry.Error = p.Err.Error()
		}
		// Omit the full list for large providers to keep the response lean.
		if entry.Count > adapters.ModelListCap {
			entry.Models = nil
		}
		providers[i] = entry
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"active":    active,
		"providers": providers,
	})
}

// ─── Serialisation helper ─────────────────────────────────────────────────────

func buildCompletePayload(responses []models.ModelResponse) []responseData {
	result := make([]responseData, len(responses))
	for i, resp := range responses {
		writes := make([]writeData, len(resp.ProposedWrites))
		for j, fw := range resp.ProposedWrites {
			fd := diff.Compute(fw.Path, fw.Content)
			lines := make([]diffLineData, len(fd.Lines))
			for k, l := range fd.Lines {
				lines[k] = diffLineData{Kind: string(l.Kind), Content: l.Content}
			}
			writes[j] = writeData{
				Path:    fw.Path,
				Content: fw.Content,
				Diff: fileDiffData{
					Path:      fd.Path,
					IsNew:     fd.IsNew,
					Adds:      fd.Adds,
					Removes:   fd.Removes,
					Lines:     lines,
					Truncated: fd.Truncated,
				},
			}
		}
		result[i] = responseData{
			ModelID:             resp.ModelID,
			Text:                resp.Text,
			LatencyMS:           resp.LatencyMS,
			InputTokens:         resp.InputTokens,
			OutputTokens:        resp.OutputTokens,
			CostUSD:             resp.CostUSD,
			ContextWindowTokens: pricing.ContextWindowTokens(resp.ModelID),
			Error:               resp.Error,
			ProposedWrites:      writes,
		}
	}
	return result
}
