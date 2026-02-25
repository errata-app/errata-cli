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
	"github.com/suarezc/errata/internal/commands"
	"github.com/suarezc/errata/internal/diff"
	"github.com/suarezc/errata/internal/history"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
	"github.com/suarezc/errata/internal/pricing"
	"github.com/suarezc/errata/internal/runner"
	"github.com/suarezc/errata/internal/tools"
)

// ─── WebSocket message types ──────────────────────────────────────────────────

// ModelSpec pairs a model ID with its originating provider name,
// allowing the server to route to the correct adapter regardless of ID prefix.
type ModelSpec struct {
	ID       string `json:"id"`
	Provider string `json:"provider,omitempty"`
}

// wsClientMsg is a message sent from the browser to the server.
type wsClientMsg struct {
	Type       string      `json:"type"`              // "run" | "select" | "cancel" | "set_models" | "set_tools"
	Prompt     string      `json:"prompt,omitempty"`
	ModelID    string      `json:"model_id,omitempty"`
	ModelSpecs []ModelSpec `json:"model_ids,omitempty"`
	Verbose    bool        `json:"verbose,omitempty"`
	Disabled   []string    `json:"disabled,omitempty"` // for set_tools: list of disabled tool names
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

// wsConn holds the per-connection state for a single browser WebSocket session.
type wsConn struct {
	s    *Server
	ctx  context.Context
	send func(wsServerMsg)

	mu             sync.Mutex
	cancelRun      context.CancelFunc     // cancel for the most recent run; nil initially
	lastRun        []models.ModelResponse // results of last completed run
	lastPrompt     string
	activeAdapters []models.ModelAdapter // nil = use all s.adapters
	disabledTools  map[string]bool       // tools excluded from runs; nil = all enabled
}

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

	wc := &wsConn{
		s:   s,
		ctx: ctx,
		send: func(msg wsServerMsg) {
			select {
			case writeCh <- msg:
			case <-ctx.Done():
			}
		},
	}

	for {
		var msg wsClientMsg
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			break
		}
		switch msg.Type {
		case "run":
			wc.wsHandleRun(msg)
		case "select":
			wc.wsHandleSelect(msg)
		case "rate_bad":
			wc.wsHandleRateBad(msg)
		case "set_models":
			wc.wsHandleSetModels(msg)
		case "cancel":
			wc.wsHandleCancel()
		case "compact":
			wc.wsHandleCompact()
		case "clear_history":
			wc.wsHandleClearHistory()
		case "set_tools":
			wc.wsHandleSetTools(msg)
		}
	}
}

func (wc *wsConn) wsHandleRun(msg wsClientMsg) {
	if strings.TrimSpace(msg.Prompt) == "" {
		wc.send(wsServerMsg{Type: "error", Message: "prompt required"})
		return
	}

	toRun := wc.activeAdapters
	if toRun == nil {
		toRun = wc.s.adapters
	}

	wc.mu.Lock()
	oldCancel := wc.cancelRun
	runCtx, cancel := context.WithTimeout(wc.ctx, 5*time.Minute)
	wc.cancelRun = cancel
	wc.mu.Unlock()

	wc.s.histMu.RLock()
	histSnapshot := make(map[string][]models.ConversationTurn, len(wc.s.histories))
	for k, v := range wc.s.histories {
		histSnapshot[k] = v
	}
	wc.s.histMu.RUnlock()

	if oldCancel != nil {
		oldCancel()
	}

	activeDefs := tools.ActiveDefinitions(wc.disabledTools)
	activeDefs = append(activeDefs, wc.s.mcpDefs...)
	mcpDispatchers := wc.s.mcpDispatchers

	go func(prompt string, runCtx context.Context, cancel context.CancelFunc, verbose bool, ads []models.ModelAdapter, hists map[string][]models.ConversationTurn) {
		defer cancel()

		effectiveHistories := hists
		for _, ad := range ads {
			if runner.ShouldAutoCompact(effectiveHistories, ad.ID()) {
				wc.send(wsServerMsg{Type: "agent_event", ModelID: ad.ID(), EventType: "text", Data: "[auto-compacting history…]"})
				effectiveHistories = runner.CompactHistories(
					runCtx, []models.ModelAdapter{ad},
					effectiveHistories, func(modelID string, e models.AgentEvent) {
						wc.send(wsServerMsg{Type: "agent_event", ModelID: modelID, EventType: e.Type, Data: e.Data})
					},
				)
			}
		}

		toolCtx := tools.WithActiveTools(runCtx, activeDefs)
		toolCtx = tools.WithMCPDispatchers(toolCtx, mcpDispatchers)
		rs := runner.RunAll(
			toolCtx, ads, effectiveHistories, prompt,
			func(modelID string, e models.AgentEvent) {
				wc.send(wsServerMsg{Type: "agent_event", ModelID: modelID, EventType: e.Type, Data: e.Data})
			},
			verbose,
		)

		if runCtx.Err() != nil {
			wc.send(wsServerMsg{Type: "cancelled"})
			return
		}

		adapterIDs := make([]string, len(ads))
		for i, ad := range ads {
			adapterIDs[i] = ad.ID()
		}
		wc.mu.Lock()
		wc.lastRun = rs
		wc.lastPrompt = prompt
		wc.mu.Unlock()

		wc.s.histMu.Lock()
		wc.s.histories = runner.AppendHistory(effectiveHistories, adapterIDs, rs, prompt)
		if err := history.Save(wc.s.histPath, wc.s.histories); err != nil {
			log.Printf("web: could not save history: %v", err)
		}
		wc.s.histMu.Unlock()

		wc.send(wsServerMsg{Type: "complete", Responses: buildCompletePayload(rs)})
	}(msg.Prompt, runCtx, cancel, msg.Verbose, toRun, histSnapshot)
}

func (wc *wsConn) wsHandleSelect(msg wsClientMsg) {
	wc.mu.Lock()
	rs := wc.lastRun
	prompt := wc.lastPrompt
	wc.mu.Unlock()

	if rs == nil {
		wc.send(wsServerMsg{Type: "error", Message: "no completed run to select from"})
		return
	}

	var selected *models.ModelResponse
	for i := range rs {
		if rs[i].ModelID == msg.ModelID {
			selected = &rs[i]
			break
		}
	}
	if selected == nil {
		wc.send(wsServerMsg{Type: "error", Message: "model not found"})
		return
	}

	if err := tools.ApplyWrites(selected.ProposedWrites); err != nil {
		wc.send(wsServerMsg{Type: "error", Message: err.Error()})
		return
	}

	if err := preferences.Record(wc.s.prefPath, prompt, msg.ModelID, wc.s.sessionID, rs); err != nil {
		log.Printf("web: could not record preference: %v", err)
	}

	wc.mu.Lock()
	wc.lastRun = nil
	wc.mu.Unlock()

	applied := make([]string, 0, len(selected.ProposedWrites))
	for _, fw := range selected.ProposedWrites {
		applied = append(applied, fw.Path)
	}
	wc.send(wsServerMsg{Type: "applied", Applied: applied})
}

func (wc *wsConn) wsHandleRateBad(msg wsClientMsg) {
	wc.mu.Lock()
	rs := wc.lastRun
	prompt := wc.lastPrompt
	wc.mu.Unlock()

	if rs == nil {
		wc.send(wsServerMsg{Type: "error", Message: "no completed run to rate"})
		return
	}

	if err := preferences.RecordBad(wc.s.prefPath, prompt, msg.ModelID, wc.s.sessionID, rs); err != nil {
		log.Printf("web: could not record bad rating: %v", err)
	}

	wc.mu.Lock()
	wc.lastRun = nil
	wc.mu.Unlock()

	wc.send(wsServerMsg{Type: "rated", ModelID: msg.ModelID})
}

func (wc *wsConn) wsHandleSetModels(msg wsClientMsg) {
	if len(msg.ModelSpecs) == 0 {
		wc.activeAdapters = nil
		ids := make([]string, len(wc.s.adapters))
		for i, a := range wc.s.adapters {
			ids[i] = a.ID()
		}
		wc.send(wsServerMsg{Type: "models_set", Models: ids})
		return
	}

	var selected []models.ModelAdapter
	var errMsgs []string
	for _, spec := range msg.ModelSpecs {
		spec.ID = strings.TrimSpace(spec.ID)
		if spec.ID == "" {
			continue
		}
		var found models.ModelAdapter
		for _, a := range wc.s.adapters {
			if a.ID() == spec.ID {
				found = a
				break
			}
		}
		if found == nil {
			a, err := adapters.NewAdapterForProvider(spec.ID, spec.Provider, wc.s.cfg)
			if err != nil {
				errMsgs = append(errMsgs, "unknown model: "+spec.ID)
				continue
			}
			found = a
		}
		selected = append(selected, found)
	}
	if len(errMsgs) > 0 {
		wc.send(wsServerMsg{Type: "error", Message: strings.Join(errMsgs, "; ")})
		if len(selected) == 0 {
			return
		}
	}
	wc.activeAdapters = selected
	ids := make([]string, len(selected))
	for i, a := range selected {
		ids[i] = a.ID()
	}
	wc.send(wsServerMsg{Type: "models_set", Models: ids})
}

func (wc *wsConn) wsHandleSetTools(msg wsClientMsg) {
	if len(msg.Disabled) == 0 {
		wc.disabledTools = nil
	} else {
		wc.disabledTools = make(map[string]bool, len(msg.Disabled))
		for _, name := range msg.Disabled {
			wc.disabledTools[name] = true
		}
	}
	// Reply with the names of currently active tools so the client can sync state.
	active := tools.ActiveDefinitions(wc.disabledTools)
	names := make([]string, len(active))
	for i, d := range active {
		names[i] = d.Name
	}
	wc.send(wsServerMsg{Type: "tools_set", Models: names})
}

func (wc *wsConn) wsHandleCancel() {
	wc.mu.Lock()
	cancel := wc.cancelRun
	wc.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (wc *wsConn) wsHandleCompact() {
	wc.mu.Lock()
	toCompact := wc.activeAdapters
	if toCompact == nil {
		toCompact = wc.s.adapters
	}
	wc.mu.Unlock()

	wc.s.histMu.RLock()
	histsToCompact := make(map[string][]models.ConversationTurn, len(wc.s.histories))
	for k, v := range wc.s.histories {
		histsToCompact[k] = v
	}
	wc.s.histMu.RUnlock()

	go func() {
		updated := runner.CompactHistories(
			wc.ctx, toCompact, histsToCompact,
			func(modelID string, e models.AgentEvent) {
				wc.send(wsServerMsg{Type: "agent_event", ModelID: modelID, EventType: e.Type, Data: e.Data})
			},
		)
		wc.s.histMu.Lock()
		wc.s.histories = updated
		if err := history.Save(wc.s.histPath, wc.s.histories); err != nil {
			log.Printf("web: could not save history: %v", err)
		}
		wc.s.histMu.Unlock()
		wc.send(wsServerMsg{Type: "compact_complete"})
	}()
}

func (wc *wsConn) wsHandleClearHistory() {
	wc.s.histMu.Lock()
	wc.s.histories = nil
	if err := history.Clear(wc.s.histPath); err != nil {
		log.Printf("web: could not clear history: %v", err)
	}
	wc.s.histMu.Unlock()
	wc.send(wsServerMsg{Type: "history_cleared"})
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

func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Name string `json:"name"`
		Desc string `json:"desc"`
	}
	cmds := commands.Web()
	out := make([]entry, len(cmds))
	for i, c := range cmds {
		out[i] = entry{c.Name, c.Desc}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// ─── Available models handler ─────────────────────────────────────────────────

// modelEntry is a single model in the /api/available-models response.
// InputPMT / OutputPMT are USD per million tokens; omitted when unknown.
type modelEntry struct {
	ID        string  `json:"id"`
	InputPMT  float64 `json:"input_pmt,omitempty"`
	OutputPMT float64 `json:"output_pmt,omitempty"`
}

// providerModelsResult is the per-provider payload returned by /api/available-models.
// Count is the filtered model count (after chat-only filter for OpenAI/Gemini);
// TotalCount is the raw API count before filtering.
type providerModelsResult struct {
	Name       string       `json:"name"`
	Models     []modelEntry `json:"models"`
	Count      int          `json:"count"`
	TotalCount int          `json:"total_count"`
	Error      string       `json:"error,omitempty"`
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
			Count:      len(p.Models),
			TotalCount: p.TotalCount,
		}
		if p.Err != nil {
			entry.Error = p.Err.Error()
		}
		ids := p.Models
		entries := make([]modelEntry, len(ids))
		for j, id := range ids {
			e := modelEntry{ID: id}
			qid := pricing.ProviderQualifiedID(p.Provider, id)
			if in, out, ok := pricing.PricingFor(qid); ok {
				e.InputPMT, e.OutputPMT = in, out
			}
			entries[j] = e
		}
		entry.Models = entries
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
		errText := resp.Error
		if runner.IsContextOverflowError(errText) {
			errText += " — use /clear or /compact to reset"
		}
		result[i] = responseData{
			ModelID:             resp.ModelID,
			Text:                resp.Text,
			LatencyMS:           resp.LatencyMS,
			InputTokens:         resp.InputTokens,
			OutputTokens:        resp.OutputTokens,
			CostUSD:             resp.CostUSD,
			ContextWindowTokens: pricing.ContextWindowTokens(resp.ModelID),
			Error:               errText,
			ProposedWrites:      writes,
		}
	}
	return result
}
