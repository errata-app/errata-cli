package adapters

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"

	"google.golang.org/genai"

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
	if cfg.BedrockRegion != "" {
		region := cfg.BedrockRegion
		jobs = append(jobs, job{"Bedrock", func() ([]string, int, error) {
			return listBedrockModels(ctx, region)
		}})
	}
	if cfg.AzureOpenAIAPIKey != "" && cfg.AzureOpenAIEndpoint != "" {
		jobs = append(jobs, job{"Azure OpenAI", func() ([]string, int, error) {
			// Azure deployments are user-managed; no public listing API.
			return nil, 0, nil
		}})
	}
	if cfg.VertexAIProject != "" && cfg.VertexAILocation != "" {
		project, location := cfg.VertexAIProject, cfg.VertexAILocation
		jobs = append(jobs, job{"Vertex AI", func() ([]string, int, error) {
			return listVertexAIModels(ctx, project, location)
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

// ─── shared HTTP helpers ──────────────────────────────────────────────────────

// doGetJSON issues a GET to url, calls configReq (if non-nil) to add headers,
// and decodes the JSON response body into dst.
func doGetJSON(ctx context.Context, url string, configReq func(*http.Request), dst any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	if configReq != nil {
		configReq(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dst)
}

// fetchDataIDs issues a GET and decodes the common {"data":[{"id":"..."}]} shape,
// returning the list of IDs.
func fetchDataIDs(ctx context.Context, url string, configReq func(*http.Request)) ([]string, error) {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doGetJSON(ctx, url, configReq, &body); err != nil {
		return nil, err
	}
	ids := make([]string, len(body.Data))
	for i, m := range body.Data {
		ids[i] = m.ID
	}
	return ids, nil
}

// ─── per-provider listing ─────────────────────────────────────────────────────

func listAnthropicModels(ctx context.Context, apiKey string) ([]string, int, error) {
	ids, err := fetchDataIDs(ctx, "https://api.anthropic.com/v1/models", func(r *http.Request) {
		r.Header.Set("x-api-key", apiKey)
		r.Header.Set("anthropic-version", "2023-06-01")
	})
	return ids, len(ids), err
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
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doGetJSON(ctx, "https://api.openai.com/v1/models", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+apiKey)
	}, &body); err != nil {
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
	var body struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey
	if err := doGetJSON(ctx, url, nil, &body); err != nil {
		return nil, 0, err
	}

	// Filter to models that support generateContent; track raw total for display.
	total := len(body.Models)
	var ids []string
	for _, m := range body.Models {
		if slices.Contains(m.SupportedGenerationMethods, "generateContent") {
			ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
		}
	}
	sort.Strings(ids)
	return ids, total, nil
}

func listOpenRouterModels(ctx context.Context, apiKey string) ([]string, int, error) {
	ids, err := fetchDataIDs(ctx, "https://openrouter.ai/api/v1/models", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+apiKey)
	})
	sort.Strings(ids)
	return ids, len(ids), err
}

func listLiteLLMModels(ctx context.Context, baseURL, apiKey string) ([]string, int, error) {
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	ids, err := fetchDataIDs(ctx, url, func(r *http.Request) {
		if apiKey != "" {
			r.Header.Set("Authorization", "Bearer "+apiKey)
		}
	})
	if err != nil {
		return nil, 0, err
	}
	for i, id := range ids {
		ids[i] = "litellm/" + id
	}
	sort.Strings(ids)
	return ids, len(ids), nil
}

func listBedrockModels(ctx context.Context, region string) ([]string, int, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, 0, err
	}
	client := bedrock.NewFromConfig(awsCfg)

	result, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
	if err != nil {
		return nil, 0, err
	}

	total := len(result.ModelSummaries)
	var ids []string
	for _, m := range result.ModelSummaries {
		// Only include models that support the Converse inference type.
		supportsConverse := false
		for _, it := range m.InferenceTypesSupported {
			if it == bedrocktypes.InferenceTypeOnDemand {
				supportsConverse = true
				break
			}
		}
		if supportsConverse && m.ModelId != nil {
			ids = append(ids, "bedrock/"+*m.ModelId)
		}
	}
	sort.Strings(ids)
	return ids, total, nil
}

func listVertexAIModels(ctx context.Context, project, location string) ([]string, int, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, 0, err
	}

	page, err := client.Models.List(ctx, nil)
	if err != nil {
		return nil, 0, err
	}

	var all []string
	for {
		for _, model := range page.Items {
			if model == nil {
				continue
			}
			// Filter to models that support generateContent.
			supportsGenerate := false
			for _, m := range model.SupportedActions {
				if m == "generateContent" {
					supportsGenerate = true
					break
				}
			}
			if supportsGenerate {
				name := model.Name
				// Strip "models/" or "publishers/google/models/" prefix.
				if i := strings.LastIndex(name, "/"); i >= 0 {
					name = name[i+1:]
				}
				all = append(all, "vertex/"+name)
			}
		}
		if page.NextPageToken == "" {
			break
		}
		page, err = page.Next(ctx)
		if err != nil {
			break // return what we have
		}
	}
	sort.Strings(all)
	return all, len(all), nil
}
