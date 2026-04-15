package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Contract tests validate that CLI API types and HTTP calls
// conform to the backend OpenAPI spec (errata-backend/api/openapi.yaml).

// ── Request schema contracts (CLI → Server) ─────────────────────────────────

func TestContract_PreferenceUploadFields(t *testing.T) {
	// Spec: PreferenceUpload { sessions (required), recipes (optional map) }
	now := time.Now().Truncate(time.Second)
	payload := PreferenceUpload{
		Recipes: map[string]string{"rcp_abc": "# Recipe\n"},
		Sessions: []SessionUpload{
			{
				ID:           "ses_1",
				CreatedAt:    now,
				LastActiveAt: now,
				Runs:         []RunUpload{{Timestamp: now, Metrics: RunMetrics{PromptHash: "ph_1", Models: []string{"m1"}}}},
			},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	// Required field present.
	assert.Contains(t, raw, "sessions")
	// Spec-defined optional field.
	assert.Contains(t, raw, "recipes")

	// Only spec-defined keys.
	specKeys := map[string]bool{"sessions": true, "recipes": true}
	for k := range raw {
		assert.True(t, specKeys[k], "PreferenceUpload has field %q not in spec", k)
	}
}

func TestContract_PreferenceUploadNoRecipeSingular(t *testing.T) {
	// Spec defines "recipes" (map) only — no singular "recipe" field.
	payload := PreferenceUpload{
		Sessions: []SessionUpload{{ID: "ses_1", Runs: []RunUpload{}}},
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	_, hasRecipe := raw["recipe"]
	assert.False(t, hasRecipe, "PreferenceUpload must not contain singular 'recipe' field")
}

func TestContract_SessionUploadFields(t *testing.T) {
	// Spec: SessionUpload required: [id, created_at, last_active_at, runs]
	// Optional: models, prompt_count, config_hash, recipe_name
	now := time.Now().Truncate(time.Second)
	s := SessionUpload{
		ID:           "ses_1",
		CreatedAt:    now,
		LastActiveAt: now,
		Models:       []string{"m1"},
		PromptCount:  5,
		ConfigHash:   "rcp_v1_abc",
		RecipeName:   "Test",
		Runs:         []RunUpload{{Timestamp: now, Metrics: RunMetrics{PromptHash: "ph_1"}}},
	}

	data, err := json.Marshal(s)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	for _, key := range []string{"id", "created_at", "last_active_at", "runs"} {
		assert.Contains(t, raw, key, "SessionUpload missing spec-required field %q", key)
	}

	specKeys := map[string]bool{
		"id": true, "created_at": true, "last_active_at": true, "runs": true,
		"models": true, "prompt_count": true, "config_hash": true, "recipe_name": true,
	}
	for k := range raw {
		assert.True(t, specKeys[k], "SessionUpload has field %q not in spec", k)
	}
}

func TestContract_SessionUploadRequiredFieldsNonOmittable(t *testing.T) {
	// Spec-required fields must be present even at zero values.
	s := SessionUpload{ID: "ses_1", Runs: []RunUpload{}}

	data, err := json.Marshal(s)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	for _, key := range []string{"id", "created_at", "last_active_at", "runs"} {
		assert.Contains(t, raw, key, "SessionUpload must always include required field %q", key)
	}
}

func TestContract_RunUploadFields(t *testing.T) {
	// Spec: RunUpload required: [timestamp]
	// Optional: type (enum: "", "rewind"), config_hash, metrics (RawMessage), content (RawMessage)
	now := time.Now().Truncate(time.Second)
	r := RunUpload{
		Timestamp:  now,
		Type:       "rewind",
		ConfigHash: "rcp_v1_abc",
		Metrics:    RunMetrics{PromptHash: "ph_1", Models: []string{"m1"}},
		Content:    &RunContentUpload{Prompt: "fix bug", Models: []ModelRunContentUpload{{ModelID: "m1", Text: "done"}}},
	}

	data, err := json.Marshal(r)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "timestamp", "RunUpload missing required field 'timestamp'")

	specKeys := map[string]bool{
		"timestamp": true, "type": true, "config_hash": true, "metrics": true, "content": true,
	}
	for k := range raw {
		assert.True(t, specKeys[k], "RunUpload has field %q not in spec", k)
	}
}

func TestContract_RunUploadTypeEnum(t *testing.T) {
	// Spec: type enum ["", "rewind"]
	for _, validType := range []string{"", "rewind"} {
		r := RunUpload{Timestamp: time.Now(), Type: validType}
		data, err := json.Marshal(r)
		require.NoError(t, err)

		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &raw))

		if validType == "" {
			_, hasType := raw["type"]
			assert.False(t, hasType, "type=\"\" should be omitted (omitempty)")
		} else {
			var got string
			require.NoError(t, json.Unmarshal(raw["type"], &got))
			assert.Equal(t, validType, got)
		}
	}
}

// ── Response schema contracts (Server → CLI) ─────────────────────────────────

func TestContract_UserResponse(t *testing.T) {
	// Spec: User { id, username, display_name, avatar_url } — all required
	specJSON := `{
		"id": "u_550e8400-e29b-41d4-a716-446655440000",
		"username": "alice",
		"display_name": "Alice Smith",
		"avatar_url": "https://avatars.githubusercontent.com/u/12345"
	}`

	var u User
	require.NoError(t, json.Unmarshal([]byte(specJSON), &u))
	assert.Equal(t, "u_550e8400-e29b-41d4-a716-446655440000", u.ID)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, "Alice Smith", u.DisplayName)
	assert.Equal(t, "https://avatars.githubusercontent.com/u/12345", u.AvatarURL)
}

func TestContract_RecipeResponse(t *testing.T) {
	// Spec: Recipe includes required "public" boolean and optional "author_username".
	specJSON := `{
		"id": "r_550e8400-e29b-41d4-a716-446655440000",
		"name": "Go Best Practices",
		"slug": "go-best-practices",
		"author_id": "a_550e8400-e29b-41d4-a716-446655440000",
		"content": "# Go Best Practices\nversion: 1\n",
		"content_hash": "abc123def456",
		"metadata": {"tags": ["go", "testing"]},
		"parser_version": 2,
		"created_at": "2024-01-15T10:30:00Z",
		"updated_at": "2024-06-20T14:00:00Z",
		"public": true,
		"author_username": "alice"
	}`

	var e RecipeEntry
	require.NoError(t, json.Unmarshal([]byte(specJSON), &e))
	assert.Equal(t, "r_550e8400-e29b-41d4-a716-446655440000", e.ID)
	assert.Equal(t, "Go Best Practices", e.Name)
	assert.Equal(t, "go-best-practices", e.Slug)
	assert.Equal(t, "alice", e.AuthorUsername)
	assert.Equal(t, 2, e.ParserVersion)
	assert.True(t, e.Public, "RecipeEntry must capture spec 'public' field")

	// Round-trip: "public" must survive marshal → unmarshal.
	redata, err := json.Marshal(e)
	require.NoError(t, err)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(redata, &raw))
	assert.Contains(t, raw, "public", "RecipeEntry must include 'public' in serialized JSON")
}

func TestContract_ReportResponse(t *testing.T) {
	// Spec: ReportResponse { id, recipe_id } — both required
	specJSON := `{
		"id": "rpt_550e8400-e29b-41d4-a716-446655440000",
		"recipe_id": "rec_550e8400-e29b-41d4-a716-446655440000"
	}`

	var r ReportUploadResult
	require.NoError(t, json.Unmarshal([]byte(specJSON), &r))
	assert.Equal(t, "rpt_550e8400-e29b-41d4-a716-446655440000", r.ID)
	assert.Equal(t, "rec_550e8400-e29b-41d4-a716-446655440000", r.RecipeID)
}

func TestContract_PreferenceUploadResponse(t *testing.T) {
	// Spec: PreferenceUploadResponse { accepted (int, required) }
	specJSON := `{"accepted": 42}`
	var result struct {
		Accepted int `json:"accepted"`
	}
	require.NoError(t, json.Unmarshal([]byte(specJSON), &result))
	assert.Equal(t, 42, result.Accepted)
}

// ── HTTP endpoint contracts ─────────────────────────────────────────────────

func TestContract_Endpoint_Me(t *testing.T) {
	// Spec: GET /api/v1/auth/me, Authorization: Bearer <token>
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(User{ID: "u1", Username: "a"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok123", httpClient: srv.Client()}
	_, _ = c.Me()

	assert.Equal(t, "GET", gotMethod)
	assert.Equal(t, "/api/v1/auth/me", gotPath)
	assert.Equal(t, "Bearer tok123", gotAuth)
}

func TestContract_Endpoint_Logout(t *testing.T) {
	// Spec: POST /api/v1/auth/logout → 204
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	require.NoError(t, SaveToken("tok"))

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	_ = c.Logout()

	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "/api/v1/auth/logout", gotPath)
}

func TestContract_Endpoint_GetRecipeRaw(t *testing.T) {
	// Spec: GET /api/v1/recipes/{id}/raw (also GET /api/v1/recipes/{username}/{slug}/raw)
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte("# Recipe\n"))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}

	_, _ = c.GetRecipeRaw("alice/my-recipe")
	assert.Equal(t, "GET", gotMethod)
	assert.Equal(t, "/api/v1/recipes/alice/my-recipe/raw", gotPath)

	_, _ = c.GetRecipeRaw("r_123")
	assert.Equal(t, "/api/v1/recipes/r_123/raw", gotPath)
}

func TestContract_Endpoint_CreateRecipe(t *testing.T) {
	// Spec: POST /api/v1/recipes, Content-Type: text/markdown
	var gotMethod, gotPath, gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(RecipeEntry{ID: "r1", Slug: "test"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	_, _ = c.CreateRecipe("# My Recipe\nversion: 1\n")

	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "/api/v1/recipes", gotPath)
	assert.Equal(t, "text/markdown", gotContentType)
	assert.Equal(t, "# My Recipe\nversion: 1\n", string(gotBody))
}

func TestContract_Endpoint_UploadPreferences(t *testing.T) {
	// Spec: POST /api/v1/preferences, Content-Type: application/json
	var gotMethod, gotPath, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"accepted": 5})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	accepted, err := c.UploadPreferences(PreferenceUpload{
		Sessions: []SessionUpload{{ID: "ses_1", Runs: []RunUpload{}}},
	})
	require.NoError(t, err)

	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "/api/v1/preferences", gotPath)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, 5, accepted)
}

func TestContract_Endpoint_UploadReport(t *testing.T) {
	// Spec: POST /api/v1/reports, Content-Type: application/json
	// Accepts 200 (idempotent) or 201 (created)
	var gotMethod, gotPath, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": "rpt_1", "recipe_id": "rec_1"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	result, err := c.UploadReport(json.RawMessage(`{"id":"rpt_1","timestamp":"2024-01-01T00:00:00Z"}`))
	require.NoError(t, err)

	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "/api/v1/reports", gotPath)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "rpt_1", result.ID)
	assert.Equal(t, "rec_1", result.RecipeID)
}

// ── Auth contract ───────────────────────────────────────────────────────────

func TestContract_BearerAuth(t *testing.T) {
	// Spec: securitySchemes.bearerAuth, scheme: bearer
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(User{ID: "u1"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "my-secret-token", httpClient: srv.Client()}
	_, _ = c.Me()

	assert.Equal(t, "Bearer my-secret-token", gotAuth)
}

func TestContract_NoAuthWhenTokenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "", httpClient: srv.Client()}
	_, _ = c.Me()

	assert.Empty(t, gotAuth)
}

// ── Error response contract ─────────────────────────────────────────────────

func TestContract_ErrorResponseParsing(t *testing.T) {
	// Spec: all error responses use { "error": "message" } shape.
	codes := []int{400, 401, 403, 404, 413, 500}
	for _, code := range codes {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				json.NewEncoder(w).Encode(map[string]string{"error": "test error"})
			}))
			defer srv.Close()

			c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
			_, err := c.Me()
			require.Error(t, err)

			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, code, apiErr.StatusCode)
			assert.Equal(t, "test error", apiErr.Message)
		})
	}
}
