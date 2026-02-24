package adapters

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/suarezc/errata/internal/config"
)

// ProviderModels holds the result of querying one provider for its model catalogue.
// TotalCount is the number of models returned by the API before any chat filter is
// applied. For providers that return all models unfiltered (Anthropic, OpenRouter,
// LiteLLM), TotalCount == len(Models). For filtered providers (OpenAI, Gemini),
// TotalCount > len(Models) when non-chat models were dropped.
type ProviderModels struct {
	Provider   string
	Models     []string
	TotalCount int
	Err        error
}

// ModelListCap is the maximum number of models shown per provider in the /models
// listing. When a provider has more, the first ModelListCap are shown followed by
// a "… and N more" notice.
const ModelListCap = 10

// ListAvailableModels concurrently queries each configured provider for its
// available model IDs. Results are returned in a consistent provider order
// (Anthropic, OpenAI, Gemini, OpenRouter, LiteLLM); only providers whose
// keys are set are included.
func ListAvailableModels(ctx context.Context, cfg config.Config) []ProviderModels {
	type job struct {
		provider string
		fn       func() ([]string, int, error)
	}

	var jobs []job
	if cfg.AnthropicAPIKey != "" {
		k := cfg.AnthropicAPIKey
		jobs = append(jobs, job{"Anthropic", func() ([]string, int, error) {
			return listAnthropicModels(ctx, k)
		}})
	}
	if cfg.OpenAIAPIKey != "" {
		k := cfg.OpenAIAPIKey
		jobs = append(jobs, job{"OpenAI", func() ([]string, int, error) {
			return listOpenAIModels(ctx, k)
		}})
	}
	if cfg.GoogleAPIKey != "" {
		k := cfg.GoogleAPIKey
		jobs = append(jobs, job{"Gemini", func() ([]string, int, error) {
			return listGeminiModels(ctx, k)
		}})
	}
	if cfg.OpenRouterAPIKey != "" {
		k := cfg.OpenRouterAPIKey
		jobs = append(jobs, job{"OpenRouter", func() ([]string, int, error) {
			return listOpenRouterModels(ctx, k)
		}})
	}
	if cfg.LiteLLMBaseURL != "" {
		base, key := cfg.LiteLLMBaseURL, cfg.LiteLLMAPIKey
		jobs = append(jobs, job{"LiteLLM", func() ([]string, int, error) {
			return listLiteLLMModels(ctx, base, key)
		}})
	}

	results := make([]ProviderModels, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			ms, total, err := j.fn()
			results[i] = ProviderModels{Provider: j.provider, Models: ms, TotalCount: total, Err: err}
		}(i, j)
	}
	wg.Wait()
	return results
}

// ─── per-provider listing ─────────────────────────────────────────────────────

func listAnthropicModels(ctx context.Context, apiKey string) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/models", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}

	ids := make([]string, len(body.Data))
	for i, m := range body.Data {
		ids[i] = m.ID
	}
	return ids, len(ids), nil
}

// isOpenAIChatModel reports whether id is a chat-completion-capable OpenAI model.
// Embeddings, Whisper, DALL-E, and other non-chat models are excluded.
// Uses an allowlist: gpt-* covers all GPT families; o<digit>* covers the o-series
// reasoning models (o1, o2, o3, o4, …); chatgpt-* covers chatgpt-4o-latest etc.
func isOpenAIChatModel(id string) bool {
	if strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "chatgpt-") {
		return true
	}
	// o-series reasoning models: o1, o2, o3, o4, o1-mini, o3-mini, …
	if len(id) >= 2 && id[0] == 'o' && id[1] >= '0' && id[1] <= '9' {
		return true
	}
	return false
}

func listOpenAIModels(ctx context.Context, apiKey string) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}

	// Filter to chat-completion-relevant models only; track raw total for display.
	total := len(body.Data)
	var ids []string
	for _, m := range body.Data {
		if isOpenAIChatModel(m.ID) {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, total, nil
}

func listGeminiModels(ctx context.Context, apiKey string) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://generativelanguage.googleapis.com/v1beta/models?key="+apiKey, nil)
	if err != nil {
		return nil, 0, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var body struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}

	// Filter to models that support generateContent; track raw total for display.
	total := len(body.Models)
	var ids []string
	for _, m := range body.Models {
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
				break
			}
		}
	}
	sort.Strings(ids)
	return ids, total, nil
}

func listOpenRouterModels(ctx context.Context, apiKey string) ([]string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}

	ids := make([]string, len(body.Data))
	for i, m := range body.Data {
		ids[i] = m.ID
	}
	sort.Strings(ids)
	return ids, len(ids), nil
}

func listLiteLLMModels(ctx context.Context, baseURL, apiKey string) ([]string, int, error) {
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}

	ids := make([]string, len(body.Data))
	for i, m := range body.Data {
		ids[i] = "litellm/" + m.ID
	}
	sort.Strings(ids)
	return ids, len(ids), nil
}
