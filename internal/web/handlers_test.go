package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/models"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

type stubAdapter struct{ id string }

func (s stubAdapter) ID() string { return s.id }
func (s stubAdapter) RunAgent(_ context.Context, _ []models.ConversationTurn, _ string, _ func(models.AgentEvent)) (models.ModelResponse, error) {
	return models.ModelResponse{ModelID: s.id}, nil
}

func newTestServer(t *testing.T, adapters []models.ModelAdapter, cfg config.Config) *Server {
	t.Helper()
	return New(adapters, t.TempDir()+"/pref.jsonl", t.TempDir()+"/hist.json", cfg)
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
	if len(raw) != 2 || raw[0].(string) != "m1" || raw[1].(string) != "m2" {
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
	p := providers[0].(map[string]any)
	if p["name"] != "LiteLLM" {
		t.Errorf("provider name = %v, want LiteLLM", p["name"])
	}
	if count := int(p["count"].(float64)); count != 2 {
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
