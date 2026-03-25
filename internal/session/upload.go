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

// CollectConfigHashes scans all sessions and returns the unique non-empty
// ConfigHash values found across all runs and session headers.
func CollectConfigHashes(sessions []api.SessionUpload) []string {
	seen := make(map[string]struct{})
	for _, s := range sessions {
		if s.ConfigHash != "" {
			seen[s.ConfigHash] = struct{}{}
		}
		for _, r := range s.Runs {
			if r.ConfigHash != "" {
				seen[r.ConfigHash] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	return out
}

// CollectContentForUpload walks session content files and returns full content
// upload payloads for the given session IDs. Sessions with missing or corrupt
// content files are silently skipped.
func CollectContentForUpload(baseDir string, sessionIDs []string) []api.SessionContentUpload {
	if len(sessionIDs) == 0 {
		return nil
	}
	var out []api.SessionContentUpload
	for _, id := range sessionIDs {
		sp := PathsFor(baseDir, id)
		content, err := LoadContent(sp.ContentPath)
		if err != nil || content == nil {
			continue
		}
		if len(content.Runs) == 0 {
			continue
		}
		sc := api.SessionContentUpload{
			SessionID: id,
			Runs:      convertRuns(content.Runs),
			Histories: content.Histories,
		}
		out = append(out, sc)
	}
	return out
}

func convertRuns(runs []RunContent) []api.RunContentUpload {
	out := make([]api.RunContentUpload, len(runs))
	for i, r := range runs {
		ms := make([]api.ModelRunContentUpload, len(r.Models))
		for j, m := range r.Models {
			ms[j] = api.ModelRunContentUpload{
				ModelID:         m.ModelID,
				Text:            m.Text,
				ProposedWrites:  m.ProposedWrites,
				Events:          m.Events,
				StopReason:      m.StopReason,
				Steps:           m.Steps,
				ReasoningTokens: m.ReasoningTokens,
			}
		}
		out[i] = api.RunContentUpload{
			Prompt: r.Prompt,
			Models: ms,
		}
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
