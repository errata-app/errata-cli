package adapters_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/adapters"
	"github.com/suarezc/errata/internal/config"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// modelListResponse is the JSON shape returned by the LiteLLM /models endpoint.
type modelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// liteLLMServer starts an httptest.Server that serves GET /v1/models with the
// provided model IDs and HTTP 200 OK. The server is closed via t.Cleanup.
func liteLLMServer(t *testing.T, ids []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := modelListResponse{}
		for _, id := range ids {
			body.Data = append(body.Data, struct {
				ID string `json:"id"`
			}{ID: id})
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// rawServer starts an httptest.Server that serves every request with a verbatim
// body string and the given HTTP status code. The server is closed via t.Cleanup.
func rawServer(t *testing.T, statusCode int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// closedServer starts and immediately closes an httptest.Server so that any
// HTTP request to it produces a connection-refused error.
func closedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close immediately so connections are refused
	return srv
}

// findProvider returns the ProviderModels entry for the given provider name,
// or fails the test if it is not present.
func findProvider(t *testing.T, results []adapters.ProviderModels, provider string) adapters.ProviderModels {
	t.Helper()
	for _, pm := range results {
		if pm.Provider == provider {
			return pm
		}
	}
	t.Fatalf("provider %q not found in results (got %d entries)", provider, len(results))
	return adapters.ProviderModels{}
}

// ─── TestListAvailableModels_LiteLLM ─────────────────────────────────────────

// TestListAvailableModels_LiteLLM verifies the full ListAvailableModels flow for
// the LiteLLM provider. LiteLLM is the only provider whose base URL we control,
// making it the only one where we can redirect HTTP traffic to a local mock server.
func TestListAvailableModels_LiteLLM(t *testing.T) {
	srv := liteLLMServer(t, []string{"gpt-4o", "claude-3"})

	cfg := config.Config{
		LiteLLMBaseURL: srv.URL + "/v1",
	}

	ctx := context.Background()
	results := adapters.ListAvailableModels(ctx, cfg)

	require.Len(t, results, 1, "expected exactly one ProviderModels entry")

	pm := findProvider(t, results, "LiteLLM")
	assert.NoError(t, pm.Err, "LiteLLM provider should not report an error")

	// The adapter prepends "litellm/" to each ID and sorts the slice.
	want := []string{"litellm/claude-3", "litellm/gpt-4o"}
	sort.Strings(want)
	assert.Equal(t, want, pm.Models, "model IDs must carry the litellm/ prefix and be sorted")
	assert.Equal(t, len(want), pm.TotalCount, "TotalCount must equal len(Models) for unfiltered providers")
}

// ─── TestListAvailableModels_NoProviders ─────────────────────────────────────

// TestListAvailableModels_NoProviders confirms that an empty config produces no
// results — no panics, no spurious entries, just an empty (or nil) slice.
func TestListAvailableModels_NoProviders(t *testing.T) {
	ctx := context.Background()
	results := adapters.ListAvailableModels(ctx, config.Config{})

	assert.Empty(t, results, "empty config should produce no ProviderModels entries")
}

// ─── TestListAvailableModels_LiteLLMError ────────────────────────────────────

// TestListAvailableModels_LiteLLMError verifies behavior when the LiteLLM
// endpoint is unreachable (connection refused). The implementation does not
// inspect HTTP status codes; it only fails on network or JSON-decode errors.
// Using a closed server guarantees a real transport error, which must surface
// as a non-nil Err on the ProviderModels entry.
func TestListAvailableModels_LiteLLMError(t *testing.T) {
	srv := closedServer(t)

	cfg := config.Config{
		LiteLLMBaseURL: srv.URL + "/v1",
	}

	ctx := context.Background()
	results := adapters.ListAvailableModels(ctx, cfg)

	require.Len(t, results, 1, "expected exactly one ProviderModels entry even on error")

	pm := findProvider(t, results, "LiteLLM")
	assert.Error(t, pm.Err, "a connection-refused error should result in a non-nil Err")
	assert.Empty(t, pm.Models, "no models should be returned when the server is unreachable")
}

// ─── TestListAvailableModels_LiteLLMBadJSON ──────────────────────────────────

// TestListAvailableModels_LiteLLMBadJSON verifies that a response whose JSON
// does not match the expected shape (no "data" array) results in an empty Models
// slice with no error. The decoder succeeds on valid JSON, it just finds nothing
// to populate in the struct, so the adapter returns an empty slice gracefully.
func TestListAvailableModels_LiteLLMBadJSON(t *testing.T) {
	// Valid JSON but the wrong shape — "data" key is absent, so nothing decodes.
	srv := rawServer(t, http.StatusOK, `{"garbage": true}`)

	cfg := config.Config{
		LiteLLMBaseURL: srv.URL + "/v1",
	}

	ctx := context.Background()
	results := adapters.ListAvailableModels(ctx, cfg)

	require.Len(t, results, 1, "expected exactly one ProviderModels entry")

	pm := findProvider(t, results, "LiteLLM")
	// json.Decoder succeeds (the JSON is valid) but finds no "data" items.
	assert.NoError(t, pm.Err, "mismatched JSON shape should not produce an error")
	assert.Empty(t, pm.Models, "no models should be decoded from a mismatched JSON shape")
}
