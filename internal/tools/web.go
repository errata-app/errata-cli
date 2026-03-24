package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/errata-app/errata-cli/internal/sandbox"
	"golang.org/x/net/html"
	"golang.org/x/sync/singleflight"
)

// webFetchGroup deduplicates concurrent web_fetch calls for the same URL.
// When two models request the same URL simultaneously, only one HTTP request
// is made and both receive the identical result, preventing rate-limiting and
// ensuring consistent content across models.
var webFetchGroup singleflight.Group

// webFetchOutputLimit is the maximum bytes returned from a web_fetch call.
const webFetchOutputLimit = 50_000

// webFetchTimeout is the HTTP request timeout for web_fetch.
const webFetchTimeout = 30 * time.Second

// webFetchUserAgent mimics a real browser to avoid bot-detection pages that
// serve stripped-down content to non-browser user agents.
const webFetchUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// ExecuteWebFetch fetches a URL and returns cleaned text content.
// HTML pages are stripped to plain text. Output is capped at webFetchOutputLimit bytes.
// Concurrent calls for the same URL are deduplicated via singleflight — only one
// HTTP request goes out, and all callers receive the identical result.
func ExecuteWebFetch(ctx context.Context, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Sprintf("[error: invalid URL: %v]", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Sprintf("[error: only http/https URLs are supported, got %q]", u.Scheme)
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		sbCfg, _ := sandbox.ConfigFromContext(ctx)
		if !sbCfg.AllowLocalFetch {
			return "[error: fetching localhost URLs is disabled; set allow_local_fetch: true in recipe ## Sandbox to enable]"
		}
	}

	result, _, _ := webFetchGroup.Do(rawURL, func() (any, error) {
		return doWebFetch(rawURL), nil
	})
	s, _ := result.(string)
	return s
}

// doWebFetch performs the actual HTTP fetch. Called via singleflight so
// only one in-flight request per URL exists at any given time.
func doWebFetch(rawURL string) string {
	client := &http.Client{Timeout: webFetchTimeout}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Sprintf("[error: could not create request: %v]", err)
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("[error: fetch failed: %v]", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Sprintf("[error: HTTP %d from %s]", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(webFetchOutputLimit*4)))
	if err != nil {
		return fmt.Sprintf("[error: reading response: %v]", err)
	}

	var text string
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		text = htmlToText(string(body))
	} else {
		text = string(body)
	}

	text = strings.TrimSpace(text)
	if len(text) > webFetchOutputLimit {
		text = text[:webFetchOutputLimit] + fmt.Sprintf("\n[truncated: output exceeded %d bytes]", webFetchOutputLimit)
	}
	if text == "" {
		return "(empty response)"
	}
	return text
}

// htmlToText converts HTML to plain text by stripping tags and skipping
// script/style/head elements. Consecutive whitespace is collapsed.
func htmlToText(htmlContent string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	var sb strings.Builder
	skip := false
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return sb.String()
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, _ := tokenizer.TagName()
			switch string(tn) {
			case "script", "style", "head":
				skip = true
			}
		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			switch string(tn) {
			case "script", "style", "head":
				skip = false
			}
		case html.TextToken:
			if !skip {
				text := strings.TrimSpace(string(tokenizer.Text()))
				if text != "" {
					sb.WriteString(text)
					sb.WriteByte('\n')
				}
			}
		}
	}
}

// webSearchTimeout is the HTTP request timeout for web_search queries.
const webSearchTimeout = 10 * time.Second

// webSearchOutputLimit is the maximum bytes returned from a web_search call.
const webSearchOutputLimit = 8_000

// webSearchAPIBaseOverride, when non-empty, replaces the DuckDuckGo API URL.
// Used in tests to point at a local HTTP server.
var webSearchAPIBaseOverride string

// SetWebSearchAPIBase overrides the DuckDuckGo API base URL (for tests).
// Pass "" to reset to the default.
func SetWebSearchAPIBase(u string) { webSearchAPIBaseOverride = u }

// webSearchAPIBase returns the DuckDuckGo API base URL.
func webSearchAPIBase() string {
	if webSearchAPIBaseOverride != "" {
		return strings.TrimRight(webSearchAPIBaseOverride, "/") + "/"
	}
	return "https://api.duckduckgo.com/"
}

// ddgResponse is the top-level DuckDuckGo instant answers API response.
type ddgResponse struct {
	AbstractText   string      `json:"AbstractText"`
	AbstractURL    string      `json:"AbstractURL"`
	AbstractSource string      `json:"AbstractSource"`
	Answer         string      `json:"Answer"`
	Definition     string      `json:"Definition"`
	DefinitionURL  string      `json:"DefinitionURL"`
	RelatedTopics  []ddgTopic  `json:"RelatedTopics"`
	Results        []ddgResult `json:"Results"`
}

// ddgTopic is either a direct topic entry or a named group of sub-topics.
// A group has Name and Topics; a direct entry has Text and FirstURL.
type ddgTopic struct {
	Text     string     `json:"Text"`
	FirstURL string     `json:"FirstURL"`
	Name     string     `json:"Name"`
	Topics   []ddgTopic `json:"Topics"`
}

type ddgResult struct {
	Text     string `json:"Text"`
	FirstURL string `json:"FirstURL"`
}

// ExecuteWebSearch queries the DuckDuckGo instant answers API and returns
// a formatted plain-text result. Best for factual/definition queries.
// Not a full web index — for specific URLs, callers should prefer ExecuteWebFetch.
func ExecuteWebSearch(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "[error: query must not be empty]"
	}

	params := url.Values{
		"q":             []string{query},
		"format":        []string{"json"},
		"no_redirect":   []string{"1"},
		"no_html":       []string{"1"},
		"skip_disambig": []string{"1"},
	}
	fullURL := webSearchAPIBase() + "?" + params.Encode()

	client := &http.Client{Timeout: webSearchTimeout}
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Sprintf("[error: could not create request: %v]", err)
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("[error: search failed: %v]", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Sprintf("[error: HTTP %d from DuckDuckGo]", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(webSearchOutputLimit*4)))
	if err != nil {
		return fmt.Sprintf("[error: reading response: %v]", err)
	}

	var ddg ddgResponse
	if err := json.Unmarshal(body, &ddg); err != nil {
		return fmt.Sprintf("[error: parsing response: %v]", err)
	}

	return formatWebSearchResult(query, ddg)
}

// formatWebSearchResult renders a DuckDuckGo API response as readable plain text.
func formatWebSearchResult(query string, ddg ddgResponse) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[DuckDuckGo: %q]\n", query)

	empty := true

	if ddg.Answer != "" {
		sb.WriteString("\n")
		sb.WriteString(ddg.Answer)
		sb.WriteString("\n")
		empty = false
	}

	if ddg.AbstractText != "" {
		sb.WriteString("\n")
		sb.WriteString(ddg.AbstractText)
		if ddg.AbstractSource != "" {
			sb.WriteString(" (via " + ddg.AbstractSource + ")")
		}
		sb.WriteString("\n")
		if ddg.AbstractURL != "" {
			sb.WriteString("Source: " + ddg.AbstractURL + "\n")
		}
		empty = false
	}

	if ddg.Definition != "" {
		sb.WriteString("\nDefinition: " + ddg.Definition + "\n")
		if ddg.DefinitionURL != "" {
			sb.WriteString("Source: " + ddg.DefinitionURL + "\n")
		}
		empty = false
	}

	// Collect related topic lines, flattening groups.
	const maxTopics = 10
	var topicLines []string
	for _, t := range ddg.RelatedTopics {
		if len(topicLines) >= maxTopics {
			break
		}
		if t.Name != "" && len(t.Topics) > 0 {
			// Named group: emit a header then subtopics.
			topicLines = append(topicLines, "["+t.Name+"]")
			for _, sub := range t.Topics {
				if len(topicLines) >= maxTopics {
					break
				}
				if sub.Text != "" {
					line := "  • " + sub.Text
					if sub.FirstURL != "" {
						line += "  " + sub.FirstURL
					}
					topicLines = append(topicLines, line)
				}
			}
		} else if t.Text != "" {
			line := "• " + t.Text
			if t.FirstURL != "" {
				line += "  " + t.FirstURL
			}
			topicLines = append(topicLines, line)
		}
	}

	if len(topicLines) > 0 {
		sb.WriteString("\nRelated:\n")
		for _, line := range topicLines {
			sb.WriteString(line + "\n")
		}
		empty = false
	}

	if empty {
		return fmt.Sprintf(
			"(no instant answer found for %q)\n\n"+
				"DuckDuckGo instant answers cover factual/definition queries.\n"+
				"For code documentation, use web_fetch with the URL directly.",
			query,
		)
	}

	result := strings.TrimRight(sb.String(), "\n")
	if len(result) > webSearchOutputLimit {
		result = result[:webSearchOutputLimit] + "\n[truncated]"
	}
	return result
}
