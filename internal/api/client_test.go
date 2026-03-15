package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── token management ─────────────────────────────────────────────────────────

func TestSaveLoadDeleteToken(t *testing.T) {
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmp)
	defer os.Setenv("HOME", origHome)

	// Save
	require.NoError(t, SaveToken("test-token-123"))

	// Load
	token := LoadToken()
	assert.Equal(t, "test-token-123", token)

	// Verify file permissions
	info, err := os.Stat(filepath.Join(tmp, ".errata", "github_token"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Delete
	require.NoError(t, DeleteToken())
	assert.Empty(t, LoadToken())
}

func TestLoadToken_NotFound(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	assert.Empty(t, LoadToken())
}

// ── APIError formatting ──────────────────────────────────────────────────────

func TestAPIError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  APIError
		want string
	}{
		{"with message", APIError{StatusCode: 404, Message: "not found"}, "api: 404 not found"},
		{"without message", APIError{StatusCode: 500}, "api: 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.err.Error())
		})
	}
}

// ── RecipeEntry.Ref ──────────────────────────────────────────────────────────

func TestRecipeEntry_Ref(t *testing.T) {
	t.Run("with author and slug", func(t *testing.T) {
		e := RecipeEntry{AuthorUsername: "alice", Slug: "my-recipe"}
		assert.Equal(t, "alice/my-recipe", e.Ref())
	})
	t.Run("falls back to ID", func(t *testing.T) {
		e := RecipeEntry{ID: "uuid-123"}
		assert.Equal(t, "uuid-123", e.Ref())
	})
}

// ── SlugFromRef ──────────────────────────────────────────────────────────────

func TestSlugFromRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"alice/my-recipe", "my-recipe"},
		{"org/sub/slug", "slug"},
		{"bare-slug", "bare-slug"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			assert.Equal(t, tt.want, SlugFromRef(tt.ref))
		})
	}
}

// ── Me endpoint ──────────────────────────────────────────────────────────────

func TestMe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/auth/me", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(User{
			ID:          "u1",
			Username:    "alice",
			DisplayName: "Alice Smith",
		})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "test-token", httpClient: srv.Client()}
	user, err := c.Me()
	require.NoError(t, err)
	assert.Equal(t, "alice", user.Username)
	assert.Equal(t, "Alice Smith", user.DisplayName)
}

func TestMe_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "bad", httpClient: srv.Client()}
	_, err := c.Me()
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 401, apiErr.StatusCode)
	assert.Contains(t, apiErr.Message, "invalid token")
}

// ── GetRecipeRaw endpoint ────────────────────────────────────────────────────

func TestGetRecipeRaw_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/recipes/alice/my-recipe/raw", r.URL.Path)
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte("# My Recipe\nversion: 1\n"))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	raw, err := c.GetRecipeRaw("alice/my-recipe")
	require.NoError(t, err)
	assert.Contains(t, raw, "# My Recipe")
}

func TestGetRecipeRaw_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "recipe not found"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	_, err := c.GetRecipeRaw("nobody/missing")
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 404, apiErr.StatusCode)
}

// ── CreateRecipe endpoint ────────────────────────────────────────────────────

func TestCreateRecipe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/recipes", r.URL.Path)
		assert.Equal(t, "text/markdown", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(RecipeEntry{
			ID:             "r1",
			Name:           "Test Recipe",
			Slug:           "test-recipe",
			AuthorUsername: "alice",
		})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	entry, err := c.CreateRecipe("# Test Recipe\nversion: 1\n")
	require.NoError(t, err)
	assert.Equal(t, "alice/test-recipe", entry.Ref())
}

// ── Logout endpoint ──────────────────────────────────────────────────────────

func TestLogout_Success(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	require.NoError(t, SaveToken("to-delete"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/auth/logout", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "to-delete", httpClient: srv.Client()}
	require.NoError(t, c.Logout())
	assert.Empty(t, LoadToken())
}

// ── GetRecipe endpoint ───────────────────────────────────────────────────────

func TestGetRecipe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/recipes/alice/code-review", r.URL.Path)
		json.NewEncoder(w).Encode(RecipeEntry{
			ID:             "r2",
			Name:           "Code Review",
			Slug:           "code-review",
			AuthorUsername: "alice",
		})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	entry, err := c.GetRecipe("alice/code-review")
	require.NoError(t, err)
	assert.Equal(t, "Code Review", entry.Name)
	assert.Equal(t, "code-review", entry.Slug)
}

// ── DeleteRecipe endpoint ────────────────────────────────────────────────────

func TestDeleteRecipe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "DELETE", r.Method)
		assert.Equal(t, "/recipes/r1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	require.NoError(t, c.DeleteRecipe("r1"))
}

// ── UpdateRecipe endpoint ────────────────────────────────────────────────────

func TestUpdateRecipe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Equal(t, "/recipes/r1", r.URL.Path)
		assert.Equal(t, "text/markdown", r.Header.Get("Content-Type"))
		json.NewEncoder(w).Encode(RecipeEntry{
			ID:             "r1",
			Name:           "Updated",
			Slug:           "updated",
			AuthorUsername: "alice",
		})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "tok", httpClient: srv.Client()}
	entry, err := c.UpdateRecipe("r1", "# Updated\nversion: 1\n")
	require.NoError(t, err)
	assert.Equal(t, "Updated", entry.Name)
}

// ── No auth header when token is empty ───────────────────────────────────────

func TestNoAuthHeaderWhenTokenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "not logged in"})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, token: "", httpClient: srv.Client()}
	_, err := c.Me()
	require.Error(t, err)
}
