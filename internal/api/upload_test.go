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
		assert.Equal(t, "/preferences", r.URL.Path)
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
				ConfigHash:   "cfg_v1_abc",
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
				ConfigHash:   "cfg_v1_deadbeef",
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
						ConfigHash:          "cfg_v1_deadbeef",
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
	assert.Equal(t, "cfg_v1_deadbeef", s.ConfigHash)
	assert.True(t, s.CreatedAt.Equal(now))

	require.Len(t, s.Runs, 1)
	r := s.Runs[0]
	assert.Equal(t, "ph_abc123", r.PromptHash)
	assert.Equal(t, "claude-sonnet-4-6", r.Selected)
	assert.Equal(t, map[string]int64{"claude-sonnet-4-6": 1200, "gpt-4o": 800}, r.LatenciesMS)
	assert.Equal(t, map[string]map[string]int{"claude-sonnet-4-6": {"bash": 2}}, r.ToolCalls)
}
