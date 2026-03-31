package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadPreferences_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v1/preferences", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)

		var payload PreferenceUpload
		assert.NoError(t, json.Unmarshal(body, &payload))
		assert.Len(t, payload.Sessions, 1)
		assert.Equal(t, "ses_test", payload.Sessions[0].ID)
		assert.Len(t, payload.Sessions[0].Runs, 2)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"accepted": 2})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "test-token", httpClient: srv.Client()}
	now := time.Now().Truncate(time.Second)
	accepted, err := c.UploadPreferences(PreferenceUpload{
		Sessions: []SessionUpload{
			{
				ID:           "ses_test",
				CreatedAt:    now,
				LastActiveAt: now,
				Models:       []string{"m1", "m2"},
				PromptCount:  2,
				ConfigHash:   "rcp_v1_abc",
				RecipeName:   "Test Recipe",
				Runs: []RunUpload{
					{
						Timestamp:  now,
						PromptHash: "ph_aaa",
						Models:     []string{"m1", "m2"},
						Selected:   "m1",
					},
					{
						Timestamp:  now,
						PromptHash: "ph_bbb",
						Models:     []string{"m1", "m2"},
						Selected:   "m2",
						Rating:     "good",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, accepted)
}

func TestUploadPreferences_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "bad", httpClient: srv.Client()}
	_, err := c.UploadPreferences(PreferenceUpload{})
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 401, apiErr.StatusCode)
}

func TestUploadPreferences_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "db down"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	_, err := c.UploadPreferences(PreferenceUpload{
		Sessions: []SessionUpload{{ID: "ses_1"}},
	})
	require.Error(t, err)
}

func TestPreferenceUpload_RoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	payload := PreferenceUpload{
		Sessions: []SessionUpload{
			{
				ID:           "ses_round",
				CreatedAt:    now,
				LastActiveAt: now,
				Models:       []string{"claude-sonnet-4-6", "gpt-4o"},
				PromptCount:  3,
				ConfigHash:   "rcp_v1_deadbeef",
				RecipeName:   "My Recipe",
				Runs: []RunUpload{
					{
						Timestamp:           now,
						PromptHash:          "ph_abc123",
						Models:              []string{"claude-sonnet-4-6", "gpt-4o"},
						Selected:            "claude-sonnet-4-6",
						LatenciesMS:         map[string]int64{"claude-sonnet-4-6": 1200, "gpt-4o": 800},
						CostsUSD:            map[string]float64{"claude-sonnet-4-6": 0.01, "gpt-4o": 0.02},
						InputTokens:         map[string]int64{"claude-sonnet-4-6": 100, "gpt-4o": 120},
						OutputTokens:        map[string]int64{"claude-sonnet-4-6": 200, "gpt-4o": 180},
						ToolCalls:           map[string]map[string]int{"claude-sonnet-4-6": {"bash": 2}},
						ProposedWritesCount: map[string]int{"claude-sonnet-4-6": 3},
						ConfigHash:          "rcp_v1_deadbeef",
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var got PreferenceUpload
	require.NoError(t, json.Unmarshal(data, &got))

	require.Len(t, got.Sessions, 1)
	s := got.Sessions[0]
	assert.Equal(t, "ses_round", s.ID)
	assert.Equal(t, "My Recipe", s.RecipeName)
	assert.Equal(t, "rcp_v1_deadbeef", s.ConfigHash)
	assert.True(t, s.CreatedAt.Equal(now))

	require.Len(t, s.Runs, 1)
	r := s.Runs[0]
	assert.Equal(t, "ph_abc123", r.PromptHash)
	assert.Equal(t, "claude-sonnet-4-6", r.Selected)
	assert.Equal(t, map[string]int64{"claude-sonnet-4-6": 1200, "gpt-4o": 800}, r.LatenciesMS)
	assert.Equal(t, map[string]map[string]int{"claude-sonnet-4-6": {"bash": 2}}, r.ToolCalls)
}

func TestPreferenceUpload_RoundTripWithRecipe(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	payload := PreferenceUpload{
		Recipe: "# Test Recipe\n\n## Models\n- m1\n",
		Sessions: []SessionUpload{
			{
				ID:         "ses_cfg",
				CreatedAt:  now,
				ConfigHash: "rcp_v1_abc",
				Runs:       []RunUpload{{PromptHash: "ph_1", Models: []string{"m1"}, ConfigHash: "rcp_v1_abc"}},
			},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	// Verify JSON shape has "recipe" key.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Contains(t, raw, "recipe")
	assert.Contains(t, raw, "sessions")

	var got PreferenceUpload
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, "# Test Recipe\n\n## Models\n- m1\n", got.Recipe)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, "rcp_v1_abc", got.Sessions[0].ConfigHash)
}

func TestUploadReport_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/api/v1/reports", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), "rpt_test")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"id":        "rpt_test",
			"recipe_id": "rec_abc",
		})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "test-token", httpClient: srv.Client()}
	result, err := c.UploadReport(json.RawMessage(`{"id":"rpt_test"}`))
	require.NoError(t, err)
	assert.Equal(t, "rpt_test", result.ID)
	assert.Equal(t, "rec_abc", result.RecipeID)
}

func TestUploadReport_Idempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"id":        "rpt_dup",
			"recipe_id": "rec_abc",
		})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	result, err := c.UploadReport(json.RawMessage(`{"id":"rpt_dup"}`))
	require.NoError(t, err)
	assert.Equal(t, "rpt_dup", result.ID)
}

func TestUploadReport_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "bad", httpClient: srv.Client()}
	_, err := c.UploadReport(json.RawMessage(`{}`))
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 401, apiErr.StatusCode)
}

func TestPreferenceUpload_RoundTripWithContent(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	payload := PreferenceUpload{
		Sessions: []SessionUpload{
			{
				ID:        "ses_content",
				CreatedAt: now,
				Runs:      []RunUpload{{PromptHash: "ph_1", Models: []string{"m1"}}},
			},
		},
		Content: []SessionContentUpload{
			{
				SessionID: "ses_content",
				Runs: []RunContentUpload{
					{
						Prompt: "fix the bug",
						Models: []ModelRunContentUpload{
							{
								ModelID:         "m1",
								Text:            "I fixed the bug.",
								StopReason:      "complete",
								Steps:           3,
								ReasoningTokens: 500,
							},
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Contains(t, raw, "content")

	var got PreferenceUpload
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got.Content, 1)
	assert.Equal(t, "ses_content", got.Content[0].SessionID)
	require.Len(t, got.Content[0].Runs, 1)
	assert.Equal(t, "fix the bug", got.Content[0].Runs[0].Prompt)
	require.Len(t, got.Content[0].Runs[0].Models, 1)
	m := got.Content[0].Runs[0].Models[0]
	assert.Equal(t, "m1", m.ModelID)
	assert.Equal(t, "I fixed the bug.", m.Text)
	assert.Equal(t, "complete", m.StopReason)
	assert.Equal(t, 3, m.Steps)
	assert.Equal(t, int64(500), m.ReasoningTokens)
}

func TestPreferenceUpload_OmitsContentWhenEmpty(t *testing.T) {
	payload := PreferenceUpload{
		Sessions: []SessionUpload{{ID: "ses_1"}},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	_, hasContent := raw["content"]
	assert.False(t, hasContent, "content should be omitted when nil")
}

func TestPreferenceUpload_OmitsRecipeWhenEmpty(t *testing.T) {
	payload := PreferenceUpload{
		Sessions: []SessionUpload{{ID: "ses_1"}},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))
	_, hasRecipe := raw["recipe"]
	assert.False(t, hasRecipe, "recipe should be omitted when empty")
}

func TestReportUploadResult_RoundTrip(t *testing.T) {
	result := ReportUploadResult{
		ID:       "rpt_abc123",
		RecipeID: "rec_xyz",
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var got ReportUploadResult
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "rpt_abc123", got.ID)
	assert.Equal(t, "rec_xyz", got.RecipeID)
}
