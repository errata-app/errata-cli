package session

import (
	"log"
	"os"
	"path/filepath"
)

// StatsFilter controls which runs are included in Summarize functions.
// A nil filter matches all runs.
type StatsFilter struct {
	ConfigHash string // if non-empty, only include runs with this config hash
	SessionID  string // if non-empty, only include runs from this session
}

// ModelStats holds detailed per-model analytics derived from run summaries.
type ModelStats struct {
	Wins              int
	Losses            int     // times model was in a run where a different model was selected
	ThumbsDown        int     // times model received a single-model thumbs-down rating
	Participations    int     // times model appeared in any recorded run
	WinRate           float64 // Wins / Participations * 100; 0 if Participations == 0
	LossRate          float64 // Losses / Participations * 100
	BadRate           float64 // ThumbsDown / Participations * 100
	AvgLatencyMS      float64
	AvgCostUSD        float64
	AvgInputTokens    float64
	AvgOutputTokens   float64
	AvgToolCalls      float64
	AvgProposedWrites float64
}

// filterRewound removes normal runs that have been rewound.
// Each "rewind" run increments a skip counter for its prompt hash;
// processing newest-to-oldest, each normal run with an active skip counter is excluded.
func filterRewound(runs []RunSummary) []RunSummary {
	skipCount := map[string]int{}

	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Type == "rewind" {
			skipCount[runs[i].PromptHash]++
		}
	}

	result := make([]RunSummary, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.Type == "rewind" {
			continue
		}
		if skipCount[r.PromptHash] > 0 {
			skipCount[r.PromptHash]--
			continue
		}
		result = append(result, r)
	}

	// Reverse to restore chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func matchesFilter(r RunSummary, f *StatsFilter) bool {
	if f == nil {
		return true
	}
	if f.ConfigHash != "" && r.ConfigHash != f.ConfigHash {
		return false
	}
	// SessionID filter is handled at the caller level (session selection).
	return true
}

// SummarizeRuns returns a model_id → win_count tally from runs.
// Rewound runs are excluded.
func SummarizeRuns(runs []RunSummary, filter *StatsFilter) map[string]int {
	tally := map[string]int{}
	for _, r := range filterRewound(runs) {
		if !matchesFilter(r, filter) {
			continue
		}
		if r.Selected != "" {
			tally[r.Selected]++
		}
	}
	return tally
}

// SummarizeRunsDetailed computes per-model analytics from runs.
// Rewound runs are excluded.
func SummarizeRunsDetailed(runs []RunSummary, filter *StatsFilter) map[string]ModelStats {
	type accumulator struct {
		wins                int
		losses              int
		thumbsDown          int
		participations      int
		totalLatencyMS      int64
		totalCostUSD        float64
		totalInputTokens    int64
		totalOutputTokens   int64
		totalToolCalls      int
		totalProposedWrites int
	}

	acc := map[string]*accumulator{}
	for _, r := range filterRewound(runs) {
		if !matchesFilter(r, filter) {
			continue
		}
		for _, m := range r.Models {
			if _, ok := acc[m]; !ok {
				acc[m] = &accumulator{}
			}
			a := acc[m]
			a.participations++
			if lat, ok := r.LatenciesMS[m]; ok {
				a.totalLatencyMS += lat
			}
			if cost, ok := r.CostsUSD[m]; ok {
				a.totalCostUSD += cost
			}
			if tok, ok := r.InputTokens[m]; ok {
				a.totalInputTokens += tok
			}
			if tok, ok := r.OutputTokens[m]; ok {
				a.totalOutputTokens += tok
			}
			if tc, ok := r.ToolCalls[m]; ok {
				for _, count := range tc {
					a.totalToolCalls += count
				}
			}
			if pw, ok := r.ProposedWritesCount[m]; ok {
				a.totalProposedWrites += pw
			}
			switch {
			case r.Selected == m:
				a.wins++
			case r.Selected != "":
				a.losses++
			case r.Rating == "bad":
				a.thumbsDown++
			}
		}
	}

	result := make(map[string]ModelStats, len(acc))
	for m, a := range acc {
		var winRate, lossRate, badRate, avgLatency, avgCost float64
		var avgInputTokens, avgOutputTokens, avgToolCalls, avgProposedWrites float64
		if a.participations > 0 {
			winRate = float64(a.wins) / float64(a.participations) * 100
			lossRate = float64(a.losses) / float64(a.participations) * 100
			badRate = float64(a.thumbsDown) / float64(a.participations) * 100
			avgLatency = float64(a.totalLatencyMS) / float64(a.participations)
			avgCost = a.totalCostUSD / float64(a.participations)
			avgInputTokens = float64(a.totalInputTokens) / float64(a.participations)
			avgOutputTokens = float64(a.totalOutputTokens) / float64(a.participations)
			avgToolCalls = float64(a.totalToolCalls) / float64(a.participations)
			avgProposedWrites = float64(a.totalProposedWrites) / float64(a.participations)
		}
		result[m] = ModelStats{
			Wins:              a.wins,
			Losses:            a.losses,
			ThumbsDown:        a.thumbsDown,
			Participations:    a.participations,
			WinRate:           winRate,
			LossRate:          lossRate,
			BadRate:           badRate,
			AvgLatencyMS:      avgLatency,
			AvgCostUSD:        avgCost,
			AvgInputTokens:    avgInputTokens,
			AvgOutputTokens:   avgOutputTokens,
			AvgToolCalls:      avgToolCalls,
			AvgProposedWrites: avgProposedWrites,
		}
	}
	return result
}

// SummarizeAcrossSessions aggregates win tallies across all sessions in baseDir.
func SummarizeAcrossSessions(baseDir string, filter *StatsFilter) map[string]int {
	tally := map[string]int{}
	for _, runs := range collectRuns(baseDir, filter) {
		for model, count := range SummarizeRuns(runs, filter) {
			tally[model] += count
		}
	}
	return tally
}

// SummarizeDetailedAcrossSessions aggregates detailed stats across all sessions.
func SummarizeDetailedAcrossSessions(baseDir string, filter *StatsFilter) map[string]ModelStats {
	groups := collectRuns(baseDir, filter)
	totalRuns := 0
	for _, runs := range groups {
		totalRuns += len(runs)
	}
	allRuns := make([]RunSummary, 0, totalRuns)
	for _, runs := range groups {
		allRuns = append(allRuns, runs...)
	}
	return SummarizeRunsDetailed(allRuns, filter)
}

// collectRuns reads all session metadata files and returns their runs,
// grouped by session. If filter specifies a SessionID, only that session is loaded.
func collectRuns(baseDir string, filter *StatsFilter) [][]RunSummary {
	if filter != nil && filter.SessionID != "" {
		sp := PathsFor(baseDir, filter.SessionID)
		m, err := LoadMetadata(sp.MetadataPath)
		if err != nil || m == nil {
			return nil
		}
		return [][]RunSummary{m.Runs}
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	var result [][]RunSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(baseDir, e.Name(), "session_metadata.json")
		m, err := LoadMetadata(metaPath)
		if err != nil {
			log.Printf("stats: skipping %q: %v", e.Name(), err)
			continue
		}
		if m == nil || len(m.Runs) == 0 {
			continue
		}
		result = append(result, m.Runs)
	}
	return result
}
