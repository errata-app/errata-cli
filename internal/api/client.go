// Package api provides an HTTP client for the errata.app backend API.
// It handles authentication (GitHub OAuth token), recipe CRUD, and user info.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	BaseURL      = "https://errata.app"
	maxRecipeSize = 1 << 20 // 1 MB
)

// TokenPath returns the path to the stored GitHub auth token.
func TokenPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".errata/github_token"
	}
	return filepath.Join(home, ".errata", "github_token")
}

// LoadToken reads the stored auth token from disk.
// Returns empty string if not found or unreadable.
func LoadToken() string {
	data, err := os.ReadFile(TokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveToken writes the auth token to disk.
func SaveToken(token string) error {
	p := TokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(token+"\n"), 0o600)
}

// DeleteToken removes the stored auth token.
func DeleteToken() error {
	return os.Remove(TokenPath())
}

// User represents the authenticated user from GET /auth/me.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

// RecipeEntry represents a recipe from the backend API.
type RecipeEntry struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	AuthorID       string         `json:"author_id"`
	Content        string         `json:"content"`
	ContentHash    string         `json:"content_hash"`
	Metadata       map[string]any `json:"metadata"`
	ParserVersion  int            `json:"parser_version"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	AuthorUsername string         `json:"author_username"`
}

// Ref returns the author/slug reference string for this recipe.
func (r *RecipeEntry) Ref() string {
	if r.AuthorUsername != "" && r.Slug != "" {
		return r.AuthorUsername + "/" + r.Slug
	}
	return r.ID
}

// SlugFromRef extracts the slug (last path component) from an author/slug ref.
func SlugFromRef(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// RecipeList is the paginated response from GET /recipes.
type RecipeList struct {
	Recipes []RecipeEntry `json:"recipes"`
	Total   int           `json:"total"`
	Page    int           `json:"page"`
	PerPage int           `json:"per_page"`
}

// APIError is returned on non-2xx responses.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("api: %d %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("api: %d", e.StatusCode)
}

// Client is the errata.app API client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a client using the locally stored token.
func NewClient() *Client {
	return &Client{
		baseURL: BaseURL,
		token:   LoadToken(),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientWithToken creates a client with an explicit token.
func NewClientWithToken(token string) *Client {
	return &Client{
		baseURL: BaseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetBaseURL overrides the API base URL (for testing).
func (c *Client) SetBaseURL(u string) {
	c.baseURL = u
}

// IsLoggedIn returns true if a token is available.
func (c *Client) IsLoggedIn() bool {
	return c.token != ""
}

// do executes an HTTP request with auth and returns the response.
func (c *Client) do(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	u := c.baseURL + path
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.httpClient.Do(req)
}

// parseError extracts an error message from a non-2xx response.
// Callers are responsible for closing resp.Body (typically via defer).
func parseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var errResp struct {
		Error string `json:"error"`
	}
	msg := ""
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
		msg = errResp.Error
	} else if len(body) > 0 {
		msg = string(body)
	}
	return &APIError{StatusCode: resp.StatusCode, Message: msg}
}

// Me returns the authenticated user.
func (c *Client) Me() (*User, error) {
	resp, err := c.do("GET", "/auth/me", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var u User
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("api: decode user: %w", err)
	}
	return &u, nil
}

// Logout invalidates the current token on the server and deletes it locally.
func (c *Client) Logout() error {
	resp, err := c.do("POST", "/auth/logout", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}
	return DeleteToken()
}

// ListRecipes fetches paginated recipes with optional filters.
func (c *Client) ListRecipes(page, perPage int, author, query string) (*RecipeList, error) {
	params := url.Values{}
	if page > 0 {
		params.Set("page", fmt.Sprintf("%d", page))
	}
	if perPage > 0 {
		params.Set("per_page", fmt.Sprintf("%d", perPage))
	}
	if author != "" {
		params.Set("author", author)
	}
	if query != "" {
		params.Set("q", query)
	}
	path := "/recipes"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	resp, err := c.do("GET", path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var list RecipeList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("api: decode recipe list: %w", err)
	}
	return &list, nil
}

// GetRecipe fetches a single recipe by ID or author/slug ref.
func (c *Client) GetRecipe(ref string) (*RecipeEntry, error) {
	resp, err := c.do("GET", "/recipes/"+ref, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var entry RecipeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, fmt.Errorf("api: decode recipe: %w", err)
	}
	return &entry, nil
}

// GetRecipeRaw fetches the raw markdown content of a recipe by ID or author/slug ref.
func (c *Client) GetRecipeRaw(ref string) (string, error) {
	resp, err := c.do("GET", "/recipes/"+ref+"/raw", nil, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", parseError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRecipeSize+1))
	if err != nil {
		return "", fmt.Errorf("api: read recipe: %w", err)
	}
	return string(data), nil
}

// CreateRecipe uploads raw markdown content as a new recipe.
func (c *Client) CreateRecipe(markdown string) (*RecipeEntry, error) {
	resp, err := c.do("POST", "/recipes", strings.NewReader(markdown), "text/markdown")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, parseError(resp)
	}
	var entry RecipeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, fmt.Errorf("api: decode recipe: %w", err)
	}
	return &entry, nil
}

// UpdateRecipe replaces a recipe's content by ID.
func (c *Client) UpdateRecipe(id, markdown string) (*RecipeEntry, error) {
	resp, err := c.do("PUT", "/recipes/"+id, strings.NewReader(markdown), "text/markdown")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, parseError(resp)
	}
	var entry RecipeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
		return nil, fmt.Errorf("api: decode recipe: %w", err)
	}
	return &entry, nil
}

// DeleteRecipe deletes a recipe by ID.
func (c *Client) DeleteRecipe(id string) error {
	resp, err := c.do("DELETE", "/recipes/"+id, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return parseError(resp)
	}
	return nil
}

// AuthLoginURL returns the URL to open in a browser to start the OAuth flow.
func AuthLoginURL(cliPort int) string {
	return fmt.Sprintf("%s/auth/github?cli_port=%d", BaseURL, cliPort)
}
