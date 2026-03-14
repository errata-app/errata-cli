package tools_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/errata-app/errata-cli/internal/tools"
)

// ─── ExecuteWebFetch ──────────────────────────────────────────────────────────

func TestExecuteWebFetch_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	out := tools.ExecuteWebFetch(srv.URL)
	assert.Equal(t, "hello world", out)
}

func TestExecuteWebFetch_HTMLStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>T</title></head><body><p>visible text</p><script>alert(1)</script></body></html>`))
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	out := tools.ExecuteWebFetch(srv.URL)
	assert.Contains(t, out, "visible text")
	assert.NotContains(t, out, "<p>")
	assert.NotContains(t, out, "alert(1)")
}

func TestExecuteWebFetch_HTTP4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	out := tools.ExecuteWebFetch(srv.URL)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "404")
}

func TestExecuteWebFetch_InvalidScheme(t *testing.T) {
	out := tools.ExecuteWebFetch("file:///etc/passwd")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "http")
}

func TestExecuteWebFetch_LocalhostBlocked(t *testing.T) {
	out := tools.ExecuteWebFetch("http://localhost:9999/test")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "localhost")
}

func TestExecuteWebFetch_InvalidURL(t *testing.T) {
	out := tools.ExecuteWebFetch("not a url at all ://")
	assert.Contains(t, out, "[error:")
}

func TestExecuteWebFetch_ConcurrentSameURLDeduplicates(t *testing.T) {
	// Verify that two concurrent requests for the same URL result in exactly
	// one HTTP request (singleflight deduplication).
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("poem content"))
	}))
	defer srv.Close()
	tools.SetAllowLocalFetch(true)
	defer tools.SetAllowLocalFetch(false)

	results := make([]string, 2)
	done := make(chan struct{}, 2)
	for i := range results {
		go func() {
			results[i] = tools.ExecuteWebFetch(srv.URL)
			done <- struct{}{}
		}()
	}
	<-done
	<-done

	// Both must have gotten the same content.
	assert.Equal(t, "poem content", results[0])
	assert.Equal(t, results[0], results[1])
	// Only one HTTP request should have been made.
	assert.Equal(t, 1, requestCount, "singleflight should deduplicate concurrent requests")
}

// ─── ExecuteWebSearch ─────────────────────────────────────────────────────────

// ddgJSON builds a minimal DuckDuckGo instant-answers JSON payload for tests.
func ddgJSON(abstract, source, abstractURL, answer string, topics []map[string]string) string {
	topicsJSON := "[]"
	if len(topics) > 0 {
		var parts []string
		for _, t := range topics {
			parts = append(parts, `{"Text":"`+t["text"]+`","FirstURL":"`+t["url"]+`"}`)
		}
		topicsJSON = "[" + strings.Join(parts, ",") + "]"
	}
	return `{"AbstractText":"` + abstract + `","AbstractURL":"` + abstractURL +
		`","AbstractSource":"` + source + `","Answer":"` + answer +
		`","Definition":"","DefinitionURL":"","RelatedTopics":` + topicsJSON + `,"Results":[]}`
}

func TestExecuteWebSearch_AbstractResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("Go is a language.", "Wikipedia", "https://en.wikipedia.org/wiki/Go", "", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("golang")
	assert.Contains(t, out, "Go is a language.")
	assert.Contains(t, out, "Wikipedia")
}

func TestExecuteWebSearch_AnswerField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "42 is the answer.", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("answer to life")
	assert.Contains(t, out, "42 is the answer.")
}

func TestExecuteWebSearch_RelatedTopics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "", []map[string]string{
			{"text": "Topic A", "url": "https://example.com/a"},
			{"text": "Topic B", "url": "https://example.com/b"},
		})))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("something")
	assert.Contains(t, out, "Topic A")
	assert.Contains(t, out, "Topic B")
}

func TestExecuteWebSearch_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("xyzzy12345")
	assert.Contains(t, out, "no instant answer found")
	assert.Contains(t, out, "web_fetch")
}

func TestExecuteWebSearch_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	out := tools.ExecuteWebSearch("anything")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "500")
}

func TestExecuteWebSearch_EmptyQuery(t *testing.T) {
	out := tools.ExecuteWebSearch("")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "empty")
}

func TestExecuteWebSearch_QueryForwardedToServer(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ddgJSON("", "", "", "", nil)))
	}))
	defer srv.Close()
	tools.SetWebSearchAPIBase(srv.URL)
	defer tools.SetWebSearchAPIBase("")

	tools.ExecuteWebSearch("my test query")
	assert.Equal(t, "my test query", receivedQuery)
}
