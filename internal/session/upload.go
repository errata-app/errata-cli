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

// MergeContent loads content files from disk and attaches per-run content
// to existing session uploads. Content is matched by original run index,
// accounting for runs removed by filterRewound. Sessions with missing or
// corrupt content/metadata files are silently skipped.
func MergeContent(sessions []api.SessionUpload, baseDir string) {
	for i, s := range sessions {
		sp := PathsFor(baseDir, s.ID)
		content, err := LoadContent(sp.ContentPath)
		if err != nil || content == nil {
			continue
		}

		meta, err := LoadMetadata(sp.MetadataPath)
		if err != nil || meta == nil {
			continue
		}

		indices := survivingIndices(meta.Runs)
		for j, idx := range indices {
			if j >= len(sessions[i].Runs) || idx >= len(content.Runs) {
				break
			}
			rc := content.Runs[idx]
			sessions[i].Runs[j].Content = &api.RunContentUpload{
				Prompt: rc.Prompt,
				Models: convertModels(rc.Models),
			}
		}
	}
}

// survivingIndices returns the original indices of runs that survive
// filterRewound (same rewind-cancellation logic, but tracking indices).
func survivingIndices(runs []RunSummary) []int {
	skipCount := map[string]int{}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Type == "rewind" {
			skipCount[runs[i].PromptHash]++
		}
	}

	var result []int
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.Type == "rewind" {
			continue
		}
		if skipCount[r.PromptHash] > 0 {
			skipCount[r.PromptHash]--
			continue
		}
		result = append(result, i)
	}

	// Reverse to restore chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func convertModels(models []ModelRunContent) []api.ModelRunContentUpload {
	out := make([]api.ModelRunContentUpload, len(models))
	for i, m := range models {
		out[i] = api.ModelRunContentUpload{
			ModelID:         m.ModelID,
			Text:            m.Text,
			ProposedWrites:  m.ProposedWrites,
			Events:          m.Events,
			StopReason:      m.StopReason,
			Steps:           m.Steps,
			ReasoningTokens: m.ReasoningTokens,
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
			Timestamp:  r.Timestamp,
			Type:       r.Type,
			ConfigHash: r.ConfigHash,
			Metrics: api.RunMetrics{
				PromptHash:          r.PromptHash,
				Models:              r.Models,
				Selected:            r.Selected,
				Rating:              r.Rating,
				LatenciesMS:         r.LatenciesMS,
				CostsUSD:            r.CostsUSD,
				InputTokens:         r.InputTokens,
				OutputTokens:        r.OutputTokens,
				ToolCalls:           r.ToolCalls,
				ProposedWritesCount: r.ProposedWritesCount,
			},
		}
	}
	return out
}
