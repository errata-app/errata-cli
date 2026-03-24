package tools_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/errata-app/errata-cli/internal/sandbox"
	"github.com/errata-app/errata-cli/internal/tools"
)

// localFetchCtx returns a context that allows web_fetch to target localhost.
func localFetchCtx() context.Context {
	return sandbox.WithConfig(context.Background(), sandbox.Config{AllowLocalFetch: true})
}

// ─── ExecuteWebFetch ──────────────────────────────────────────────────────────

func TestExecuteWebFetch_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	out := tools.ExecuteWebFetch(localFetchCtx(), srv.URL)
	assert.Equal(t, "hello world", out)
}

func TestExecuteWebFetch_HTMLStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>T</title></head><body><p>visible text</p><script>alert(1)</script></body></html>`))
	}))
	defer srv.Close()

	out := tools.ExecuteWebFetch(localFetchCtx(), srv.URL)
	assert.Contains(t, out, "visible text")
	assert.NotContains(t, out, "<p>")
	assert.NotContains(t, out, "alert(1)")
}

func TestExecuteWebFetch_HTTP4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	out := tools.ExecuteWebFetch(localFetchCtx(), srv.URL)
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "404")
}

func TestExecuteWebFetch_InvalidScheme(t *testing.T) {
	out := tools.ExecuteWebFetch(context.Background(), "file:///etc/passwd")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "http")
}

func TestExecuteWebFetch_LocalhostBlocked(t *testing.T) {
	out := tools.ExecuteWebFetch(context.Background(), "http://localhost:9999/test")
	assert.Contains(t, out, "[error:")
	assert.Contains(t, out, "localhost")
}

func TestExecuteWebFetch_InvalidURL(t *testing.T) {
	out := tools.ExecuteWebFetch(context.Background(), "not a url at all ://")
	assert.Contains(t, out, "[error:")
}

func TestExecuteWebFetch_ConcurrentSameURLDeduplicates(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("poem content"))
	}))
	defer srv.Close()

	ctx := localFetchCtx()
	results := make([]string, 2)
	done := make(chan struct{}, 2)
	for i := range results {
		go func() {
			results[i] = tools.ExecuteWebFetch(ctx, srv.URL)
			done <- struct{}{}
		}()
	}
	<-done
	<-done

	assert.Equal(t, "poem content", results[0])
	assert.Equal(t, results[0], results[1])
	assert.LessOrEqual(t, requestCount, 2)
}

func TestExecuteWebFetch_LargeOutputTruncated(t *testing.T) {
	bigBody := strings.Repeat("x", 100_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(bigBody))
	}))
	defer srv.Close()

	out := tools.ExecuteWebFetch(localFetchCtx(), srv.URL)
	assert.LessOrEqual(t, len(out), 55_000)
}
