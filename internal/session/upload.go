package session

import (
	"time"

	"github.com/errata-app/errata-cli/internal/api"
)

// CollectForUpload walks session metadata and returns redacted upload payloads.
// Sessions with LastActiveAt on or before since are skipped (zero since includes all).
// nameLookup resolves a config hash to a recipe name; it may return "".
func CollectForUpload(baseDir string, since time.Time, nameLookup func(string) string) []api.SessionUpload {
	metas, err := List(baseDir)
	if err != nil {
		return nil
	}
	var out []api.SessionUpload
	for _, m := range metas {
		if !since.IsZero() && !m.LastActiveAt.After(since) {
			continue
		}
		runs := redactRuns(filterRewound(m.Runs))
		if len(runs) == 0 {
			continue
		}
		s := api.SessionUpload{
			ID:           m.ID,
			CreatedAt:    m.CreatedAt,
			LastActiveAt: m.LastActiveAt,
			Models:       m.Models,
			PromptCount:  m.PromptCount,
			ConfigHash:   m.ConfigHash,
			Runs:         runs,
		}
		if nameLookup != nil && m.ConfigHash != "" {
			s.RecipeName = nameLookup(m.ConfigHash)
		}
		out = append(out, s)
	}
	return out
}

// redactRuns converts RunSummary values to RunUpload values,
// stripping PromptPreview, AppliedFiles, and Note.
func redactRuns(runs []RunSummary) []api.RunUpload {
	if len(runs) == 0 {
		return nil
	}
	out := make([]api.RunUpload, len(runs))
	for i, r := range runs {
		out[i] = api.RunUpload{
			Timestamp:           r.Timestamp,
			PromptHash:          r.PromptHash,
			Models:              r.Models,
			Selected:            r.Selected,
			Rating:              r.Rating,
			Type:                r.Type,
			LatenciesMS:         r.LatenciesMS,
			CostsUSD:            r.CostsUSD,
			InputTokens:         r.InputTokens,
			OutputTokens:        r.OutputTokens,
			ToolCalls:           r.ToolCalls,
			ProposedWritesCount: r.ProposedWritesCount,
			ConfigHash:          r.ConfigHash,
		}
	}
	return out
}
