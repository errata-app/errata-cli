package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

type stubAdapter struct{ id string }

func (s stubAdapter) ID() string { return s.id }
func (s stubAdapter) Capabilities(_ context.Context) models.ModelCapabilities {
	return models.ModelCapabilities{}
}
func (s stubAdapter) RunAgent(_ context.Context, _ []models.ConversationTurn, _ string, _ func(models.AgentEvent)) (models.ModelResponse, error) {
	return models.ModelResponse{ModelID: s.id}, nil
}

func newTestServer(t *testing.T, adapters []models.ModelAdapter, cfg config.Config) *Server {
	t.Helper()
	return New(adapters, t.TempDir()+"/pref.jsonl", t.TempDir()+"/hist.json", cfg, nil, nil, nil, nil)
}

func decodeJSON(t *testing.T, body *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(body.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

// ─── handleStats ─────────────────────────────────────────────────────────────

func TestHandleStats_EmptyTallyReturnsEmptyMap(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleStats(w, httptest.NewRequest("GET", "/api/stats", nil))

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := decodeJSON(t, w)
	tally, ok := body["tally"].(map[string]any)
	if !ok {
		t.Fatalf("tally field missing or wrong type: %T", body["tally"])
	}
	if len(tally) != 0 {
		t.Errorf("expected empty tally, got %v", tally)
	}
}

func TestHandleStats_StatusOK(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleStats(w, httptest.NewRequest("GET", "/api/stats", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ─── handleModels ─────────────────────────────────────────────────────────────

func TestHandleModels_ReturnsAllAdapterIDs(t *testing.T) {
	ads := []models.ModelAdapter{stubAdapter{"alpha"}, stubAdapter{"beta"}, stubAdapter{"gamma"}}
	s := newTestServer(t, ads, config.Config{})
	w := httptest.NewRecorder()
	s.handleModels(w, httptest.NewRequest("GET", "/api/models", nil))

	body := decodeJSON(t, w)
	raw, ok := body["models"].([]any)
	if !ok {
		t.Fatalf("models field missing or wrong type: %T", body["models"])
	}
	got := make([]string, len(raw))
	for i, v := range raw {
		got[i], ok = v.(string)
		if !ok {
			t.Fatalf("models[%d] is not a string: %T", i, v)
		}
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("len(models) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("models[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHandleModels_NoAdaptersReturnsEmptySlice(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleModels(w, httptest.NewRequest("GET", "/api/models", nil))

	body := decodeJSON(t, w)
	raw, ok := body["models"].([]any)
	if !ok {
		t.Fatalf("models field missing or wrong type: %T", body["models"])
	}
	if len(raw) != 0 {
		t.Errorf("expected empty models, got %v", raw)
	}
}

func TestHandleModels_ContentType(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleModels(w, httptest.NewRequest("GET", "/api/models", nil))
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// ─── handleAvailableModels ───────────────────────────────────────────────────

func mockLiteLLMServer(t *testing.T, modelIDs []string) *httptest.Server {
	t.Helper()
	items := make([]string, len(modelIDs))
	for i, id := range modelIDs {
		items[i] = fmt.Sprintf(`{"id":%q}`, id)
	}
	body := `{"data":[` + strings.Join(items, ",") + `]}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
}

func TestHandleAvailableModels_ActiveFieldMatchesAdapters(t *testing.T) {
	ads := []models.ModelAdapter{stubAdapter{"m1"}, stubAdapter{"m2"}}
	s := newTestServer(t, ads, config.Config{}) // no providers configured
	w := httptest.NewRecorder()
	s.handleAvailableModels(w, httptest.NewRequest("GET", "/api/available-models", nil))

	body := decodeJSON(t, w)
	raw, ok := body["active"].([]any)
	if !ok {
		t.Fatalf("active field missing or wrong type: %T", body["active"])
	}
	s0, _ := raw[0].(string)
	s1, _ := raw[1].(string)
	if len(raw) != 2 || s0 != "m1" || s1 != "m2" {
		t.Errorf("active = %v, want [m1 m2]", raw)
	}
}

func TestHandleAvailableModels_NoProvidersReturnsEmptyProviders(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleAvailableModels(w, httptest.NewRequest("GET", "/api/available-models", nil))

	body := decodeJSON(t, w)
	providers, ok := body["providers"].([]any)
	if !ok {
		t.Fatalf("providers field missing or wrong type: %T", body["providers"])
	}
	if len(providers) != 0 {
		t.Errorf("expected zero providers, got %d", len(providers))
	}
}

func TestHandleAvailableModels_LiteLLMProviderIncluded(t *testing.T) {
	mock := mockLiteLLMServer(t, []string{"gpt-4o", "claude-3"})
	defer mock.Close()

	cfg := config.Config{LiteLLMBaseURL: mock.URL + "/v1"}
	s := newTestServer(t, nil, cfg)
	w := httptest.NewRecorder()
	s.handleAvailableModels(w, httptest.NewRequest("GET", "/api/available-models", nil))

	body := decodeJSON(t, w)
	providers, ok := body["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %v", body["providers"])
	}
	p, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatal("expected map[string]any for provider entry")
	}
	if p["name"] != "LiteLLM" {
		t.Errorf("provider name = %v, want LiteLLM", p["name"])
	}
	countVal, _ := p["count"].(float64)
	if count := int(countVal); count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestHandleAvailableModels_ContentType(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleAvailableModels(w, httptest.NewRequest("GET", "/api/available-models", nil))
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// ─── handleWS set_models ──────────────────────────────────────────────────────

func wsConnect(t *testing.T, s *Server) (context.Context, *websocket.Conn) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(s.handleWS))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return ctx, conn
}

func wsSetModels(t *testing.T, ctx context.Context, conn *websocket.Conn, specs []ModelSpec) wsServerMsg {
	t.Helper()
	if err := wsjson.Write(ctx, conn, wsClientMsg{Type: "set_models", ModelSpecs: specs}); err != nil {
		t.Fatalf("write set_models: %v", err)
	}
	var msg wsServerMsg
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return msg
}

// TestHandleWS_SetModels_ProviderTaggedNovelID verifies that a model ID with no
// recognised prefix routes to the correct adapter when a provider hint is given.
func TestHandleWS_SetModels_ProviderTaggedNovelID(t *testing.T) {
	s := newTestServer(t, nil, config.Config{OpenAIAPIKey: "sk-test"})
	ctx, conn := wsConnect(t, s)

	msg := wsSetModels(t, ctx, conn, []ModelSpec{{ID: "ricky", Provider: "OpenAI"}})
	if msg.Type != "models_set" {
		t.Fatalf("type = %q, want models_set", msg.Type)
	}
	if len(msg.Models) != 1 || msg.Models[0] != "ricky" {
		t.Errorf("models = %v, want [ricky]", msg.Models)
	}
}

// TestHandleWS_SetModels_EmptyResetsToAll verifies that an empty model_ids list
// resets the filter and returns all configured adapter IDs.
func TestHandleWS_SetModels_EmptyResetsToAll(t *testing.T) {
	ads := []models.ModelAdapter{stubAdapter{"alpha"}, stubAdapter{"beta"}}
	s := newTestServer(t, ads, config.Config{})
	ctx, conn := wsConnect(t, s)

	msg := wsSetModels(t, ctx, conn, nil)
	if msg.Type != "models_set" {
		t.Fatalf("type = %q, want models_set", msg.Type)
	}
	if len(msg.Models) != 2 {
		t.Errorf("models = %v, want [alpha beta]", msg.Models)
	}
}

// TestHandleWS_SetModels_PreferConfiguredAdapter verifies that a model already
// in s.adapters is reused rather than created on demand.
func TestHandleWS_SetModels_PreferConfiguredAdapter(t *testing.T) {
	ads := []models.ModelAdapter{stubAdapter{"alpha"}}
	s := newTestServer(t, ads, config.Config{})
	ctx, conn := wsConnect(t, s)

	msg := wsSetModels(t, ctx, conn, []ModelSpec{{ID: "alpha"}})
	if msg.Type != "models_set" {
		t.Fatalf("type = %q, want models_set", msg.Type)
	}
	if len(msg.Models) != 1 || msg.Models[0] != "alpha" {
		t.Errorf("models = %v, want [alpha]", msg.Models)
	}
}

// TestHandleWS_SetModels_UnknownModelSendsError verifies that a model with no
// provider hint and no recognised prefix produces an error message.
func TestHandleWS_SetModels_UnknownModelSendsError(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	ctx, conn := wsConnect(t, s)

	msg := wsSetModels(t, ctx, conn, []ModelSpec{{ID: "ricky"}})
	if msg.Type != "error" {
		t.Fatalf("type = %q, want error", msg.Type)
	}
	if !strings.Contains(msg.Message, "ricky") {
		t.Errorf("message = %q, want mention of 'ricky'", msg.Message)
	}
}

func TestHandleAvailableModels_ReturnsAllModelsNoCap(t *testing.T) {
	// Build a provider with more than the old ModelListCap (10) models to ensure
	// the REST handler does not truncate — display capping is the client's job.
	ids := make([]string, 15)
	for i := range ids {
		ids[i] = fmt.Sprintf("litellm/model-%02d", i)
	}
	mock := mockLiteLLMServer(t, ids)
	defer mock.Close()

	cfg := config.Config{LiteLLMBaseURL: mock.URL + "/v1"}
	s := newTestServer(t, nil, cfg)
	w := httptest.NewRecorder()
	s.handleAvailableModels(w, httptest.NewRequest("GET", "/api/available-models", nil))

	body := decodeJSON(t, w)
	providers, ok := body["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %v", body["providers"])
	}
	p, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatal("providers[0] is not map[string]any")
	}
	models, ok := p["models"].([]any)
	if !ok {
		t.Fatalf("models field missing or wrong type")
	}
	if len(models) != 15 {
		t.Errorf("got %d models, want 15 (no cap should be applied by server)", len(models))
	}
	if _, hasTruncated := p["truncated"]; hasTruncated {
		t.Errorf("truncated field should not be present in response")
	}
}

// ─── handleCommands ───────────────────────────────────────────────────────────

func TestHandleCommands_ReturnsJSONArray(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleCommands(w, httptest.NewRequest("GET", "/api/commands", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestHandleCommands_ContainsKnownCommands(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleCommands(w, httptest.NewRequest("GET", "/api/commands", nil))

	var cmds []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&cmds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cmds) == 0 {
		t.Fatal("expected non-empty command list")
	}
	// /help should always be present and not TUIOnly
	found := false
	for _, c := range cmds {
		if c["name"] == "/help" {
			found = true
			break
		}
	}
	if !found {
		t.Error("/help not found in commands response")
	}
}

func TestHandleCommands_OmitsTUIOnlyCommands(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleCommands(w, httptest.NewRequest("GET", "/api/commands", nil))

	var cmds []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&cmds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, c := range cmds {
		if c["name"] == "/exit" {
			t.Error("/exit is TUIOnly and should not appear in web commands")
		}
	}
}

// ─── wsHandleSetTools ─────────────────────────────────────────────────────────

func wsSetTools(t *testing.T, ctx context.Context, conn *websocket.Conn, disabled []string) wsServerMsg {
	t.Helper()
	if err := wsjson.Write(ctx, conn, wsClientMsg{Type: "set_tools", Disabled: disabled}); err != nil {
		t.Fatalf("write set_tools: %v", err)
	}
	var msg wsServerMsg
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return msg
}

func TestHandleWS_SetTools_DisableBash(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	ctx, conn := wsConnect(t, s)

	msg := wsSetTools(t, ctx, conn, []string{"bash"})
	if msg.Type != "tools_set" {
		t.Fatalf("type = %q, want tools_set", msg.Type)
	}
	for _, name := range msg.Models {
		if name == "bash" {
			t.Error("bash should be excluded from active tools after disabling")
		}
	}
}

func TestHandleWS_SetTools_EmptyDisabledEnablesAll(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	ctx, conn := wsConnect(t, s)

	msg := wsSetTools(t, ctx, conn, nil)
	if msg.Type != "tools_set" {
		t.Fatalf("type = %q, want tools_set", msg.Type)
	}
	if len(msg.Models) == 0 {
		t.Error("expected all tools active when disabled list is empty")
	}
}

// ─── handleToolsList ──────────────────────────────────────────────────────────

func TestHandleToolsList_ReturnsBuiltinTools(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	w := httptest.NewRecorder()
	s.handleToolsList(w, httptest.NewRequest("GET", "/api/tools", nil))

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var toolList []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&toolList); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(toolList) == 0 {
		t.Fatal("expected non-empty tools list")
	}
	// All should be builtin since no MCP defs are configured.
	for _, tool := range toolList {
		if tool["source"] != "builtin" {
			t.Errorf("expected source=builtin, got %v", tool["source"])
		}
		if tool["name"] == nil || tool["name"] == "" {
			t.Error("tool should have a name")
		}
	}
}

func TestHandleToolsList_IncludesMCPDefs(t *testing.T) {
	mcpDefs := []tools.ToolDef{
		{Name: "mcp_search", Description: "MCP search tool"},
	}
	s := New(nil, t.TempDir()+"/pref.jsonl", t.TempDir()+"/hist.json", config.Config{}, mcpDefs, nil, nil, nil)
	w := httptest.NewRecorder()
	s.handleToolsList(w, httptest.NewRequest("GET", "/api/tools", nil))

	var toolList []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&toolList); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Find the MCP tool.
	found := false
	for _, tool := range toolList {
		if tool["name"] == "mcp_search" && tool["source"] == "mcp" {
			found = true
			break
		}
	}
	if !found {
		t.Error("mcp_search tool not found in response")
	}
}

// ─── buildCompletePayload ─────────────────────────────────────────────────────

func TestBuildCompletePayload_EmptyResponses(t *testing.T) {
	result := buildCompletePayload(nil)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}

func TestBuildCompletePayload_TextOnlyResponse(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Text: "hello", LatencyMS: 100, InputTokens: 50, OutputTokens: 20, CostUSD: 0.001},
	}
	result := buildCompletePayload(responses)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	r := result[0]
	if r.ModelID != "m1" {
		t.Errorf("ModelID = %q, want m1", r.ModelID)
	}
	if r.Text != "hello" {
		t.Errorf("Text = %q, want hello", r.Text)
	}
	if r.LatencyMS != 100 {
		t.Errorf("LatencyMS = %d, want 100", r.LatencyMS)
	}
	if len(r.ProposedWrites) != 0 {
		t.Errorf("expected no writes, got %d", len(r.ProposedWrites))
	}
}

func TestBuildCompletePayload_ErrorResponse(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Error: "api error"},
	}
	result := buildCompletePayload(responses)
	if result[0].Error != "api error" {
		t.Errorf("Error = %q, want 'api error'", result[0].Error)
	}
}

func TestBuildCompletePayload_ContextOverflowError(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Error: "context_length_exceeded"},
	}
	result := buildCompletePayload(responses)
	if !strings.Contains(result[0].Error, "context_length_exceeded") {
		t.Errorf("expected original error, got %q", result[0].Error)
	}
	if !strings.Contains(result[0].Error, "/compact") {
		t.Errorf("expected /compact hint appended, got %q", result[0].Error)
	}
}

func TestBuildCompletePayload_InterruptedFlag(t *testing.T) {
	responses := []models.ModelResponse{
		{ModelID: "m1", Interrupted: true},
	}
	result := buildCompletePayload(responses)
	if !result[0].Interrupted {
		t.Error("expected Interrupted=true in payload")
	}
}

// ─── wsHandleClearHistory ─────────────────────────────────────────────────────

func TestHandleWS_ClearHistory_SendsHistoryCleared(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	ctx, conn := wsConnect(t, s)

	if err := wsjson.Write(ctx, conn, wsClientMsg{Type: "clear_history"}); err != nil {
		t.Fatalf("write clear_history: %v", err)
	}
	var msg wsServerMsg
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if msg.Type != "history_cleared" {
		t.Errorf("type = %q, want history_cleared", msg.Type)
	}
}

// ─── wsHandleClearDisplay ────────────────────────────────────────────────────

func TestHandleWS_ClearDisplay_SendsDisplayCleared(t *testing.T) {
	s := newTestServer(t, nil, config.Config{})
	ctx, conn := wsConnect(t, s)

	if err := wsjson.Write(ctx, conn, wsClientMsg{Type: "clear_display"}); err != nil {
		t.Fatalf("write clear_display: %v", err)
	}
	var msg wsServerMsg
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if msg.Type != "display_cleared" {
		t.Errorf("type = %q, want display_cleared", msg.Type)
	}
}
