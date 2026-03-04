// Package preferences manages the append-only JSONL preference log.
package preferences

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/suarezc/errata/internal/models"
)

// Entry is one recorded preference decision.
type Entry struct {
	TS                  string                       `json:"ts"`
	Type                string                       `json:"type,omitempty"` // "" for normal entries, "rewind" for rewind markers
	PromptHash          string                       `json:"prompt_hash"`
	PromptPreview       string                       `json:"prompt_preview"`
	Models              []string                     `json:"models"`
	Selected            string                       `json:"selected"`
	Rating              string                       `json:"rating,omitempty"` // "bad" for single-model thumbs-down; empty otherwise
	LatenciesMS         map[string]int64             `json:"latencies_ms"`
	CostsUSD            map[string]float64           `json:"costs_usd"`
	InputTokens         map[string]int64             `json:"input_tokens"`
	OutputTokens        map[string]int64             `json:"output_tokens"`
	ToolCalls           map[string]map[string]int    `json:"tool_calls"`
	ProposedWritesCount map[string]int               `json:"proposed_writes_count"`
	ConfigHash          string                       `json:"config_hash,omitempty"` // content-addressed config snapshot hash
	SessionID           string                       `json:"session_id"`
}

// StatsFilter controls which entries are included in Summarize/SummarizeDetailed.
// A nil filter matches all entries.
type StatsFilter struct {
	ConfigHash string // if non-empty, only include entries with this config hash
	SessionID  string // if non-empty, only include entries with this session ID
}

// Record appends one preference entry to the JSONL log at path.
func Record(path, prompt, selectedModel, configHash, sessionID string, responses []models.ModelResponse) error {
	return recordEntry(path, prompt, selectedModel, "", configHash, sessionID, responses)
}

// RecordBad appends a thumbs-down entry for a single-model response.
// Selected is left empty; Rating is set to "bad".
func RecordBad(path, prompt, modelID, configHash, sessionID string, responses []models.ModelResponse) error {
	return recordEntry(path, prompt, "", "bad", configHash, sessionID, responses)
}

// recordEntry is the shared implementation for Record and RecordBad.
func recordEntry(path, prompt, selected, rating, configHash, sessionID string, responses []models.ModelResponse) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	preview := prompt
	if len(preview) > 120 {
		preview = preview[:120]
	}

	hash := sha256.Sum256([]byte(prompt))

	modelIDs := make([]string, len(responses))
	latencies := make(map[string]int64, len(responses))
	costs := make(map[string]float64, len(responses))
	inputTokens := make(map[string]int64, len(responses))
	outputTokens := make(map[string]int64, len(responses))
	toolCallsMap := make(map[string]map[string]int, len(responses))
	proposedWritesCount := make(map[string]int, len(responses))
	for i, r := range responses {
		modelIDs[i] = r.ModelID
		latencies[r.ModelID] = r.LatencyMS
		costs[r.ModelID] = r.CostUSD
		inputTokens[r.ModelID] = r.InputTokens
		outputTokens[r.ModelID] = r.OutputTokens
		toolCallsMap[r.ModelID] = r.ToolCalls
		proposedWritesCount[r.ModelID] = len(r.ProposedWrites)
	}

	entry := Entry{
		TS:                  time.Now().UTC().Format(time.RFC3339),
		PromptHash:          fmt.Sprintf("sha256:%x", hash),
		PromptPreview:       preview,
		Models:              modelIDs,
		Selected:            selected,
		Rating:              rating,
		LatenciesMS:         latencies,
		CostsUSD:            costs,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		ToolCalls:           toolCallsMap,
		ProposedWritesCount: proposedWritesCount,
		ConfigHash:          configHash,
		SessionID:           sessionID,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintln(f, string(data))
	return err
}

// RecordRewind appends a rewind marker entry to the JSONL log.
// The marker matches the most recent normal entry with the same prompt_hash and session_id
// so that Summarize/SummarizeDetailed can exclude the rewound preference.
func RecordRewind(path, prompt, sessionID string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	hash := sha256.Sum256([]byte(prompt))

	entry := Entry{
		TS:         time.Now().UTC().Format(time.RFC3339),
		Type:       "rewind",
		PromptHash: fmt.Sprintf("sha256:%x", hash),
		SessionID:  sessionID,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintln(f, string(data))
	return err
}

// filterRewound removes normal entries that have been rewound.
// Each "rewind" entry increments a skip counter for its (prompt_hash, session_id) key;
// processing newest-to-oldest, each normal entry with an active skip counter is excluded.
func filterRewound(entries []Entry) []Entry {
	type key struct{ hash, session string }
	skipCount := map[key]int{}

	// Walk newest-to-oldest to count rewind markers.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type == "rewind" {
			skipCount[key{e.PromptHash, e.SessionID}]++
		}
	}

	// Walk newest-to-oldest to filter.
	result := make([]Entry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Type == "rewind" {
			continue
		}
		k := key{e.PromptHash, e.SessionID}
		if skipCount[k] > 0 {
			skipCount[k]--
			continue
		}
		result = append(result, e)
	}

	// Reverse to restore chronological order.
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// LoadAll reads all valid entries from the JSONL log.
// Corrupt lines are skipped with a log warning.
func LoadAll(path string) []Entry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entries []Entry
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			log.Printf("preferences: line %d is not valid JSON, skipping", i+1)
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// Summarize returns a model_id → win_count tally.
// Rewound entries are excluded from the tally.
// If filter is non-nil, only entries matching the filter are included.
func Summarize(path string, filter *StatsFilter) map[string]int {
	tally := map[string]int{}
	for _, e := range filterRewound(LoadAll(path)) {
		if !matchesFilter(e, filter) {
			continue
		}
		if e.Selected != "" {
			tally[e.Selected]++
		}
	}
	return tally
}

// matchesFilter returns true if the entry matches the given filter.
// A nil filter matches everything. Legacy entries (empty ConfigHash)
// are included in unfiltered queries, excluded when filtering by hash.
func matchesFilter(e Entry, f *StatsFilter) bool {
	if f == nil {
		return true
	}
	if f.ConfigHash != "" && e.ConfigHash != f.ConfigHash {
		return false
	}
	if f.SessionID != "" && e.SessionID != f.SessionID {
		return false
	}
	return true
}

// ModelStats holds detailed per-model analytics derived from preference entries.
type ModelStats struct {
	Wins              int
	Losses            int     // times model was in a run where a different model was selected
	ThumbsDown        int     // times model received a single-model thumbs-down rating
	Participations    int     // times model appeared in any recorded entry
	WinRate           float64 // Wins / Participations × 100; 0 if Participations == 0
	LossRate          float64 // Losses / Participations × 100
	BadRate           float64 // ThumbsDown / Participations × 100
	AvgLatencyMS      float64 // mean latency across all runs where model participated
	AvgCostUSD        float64 // mean cost; 0 for entries recorded before cost tracking
	AvgInputTokens    float64 // mean input tokens; 0 for entries without token data
	AvgOutputTokens   float64 // mean output tokens; 0 for entries without token data
	AvgToolCalls      float64 // mean total tool calls per run
	AvgProposedWrites float64 // mean proposed file writes per run
}

// SummarizeDetailed computes per-model analytics from all preference entries.
// Rewound entries are excluded. Entries recorded before cost tracking was added
// have zero cost contribution. If filter is non-nil, only matching entries are included.
func SummarizeDetailed(path string, filter *StatsFilter) map[string]ModelStats {
	type accumulator struct {
		wins               int
		losses             int
		thumbsDown         int
		participations     int
		totalLatencyMS     int64
		totalCostUSD       float64
		totalInputTokens   int64
		totalOutputTokens  int64
		totalToolCalls     int
		totalProposedWrites int
	}

	acc := map[string]*accumulator{}
	for _, e := range filterRewound(LoadAll(path)) {
		if !matchesFilter(e, filter) {
			continue
		}
		for _, m := range e.Models {
			if _, ok := acc[m]; !ok {
				acc[m] = &accumulator{}
			}
			a := acc[m]
			a.participations++
			if lat, ok := e.LatenciesMS[m]; ok {
				a.totalLatencyMS += lat
			}
			if cost, ok := e.CostsUSD[m]; ok {
				a.totalCostUSD += cost
			}
			if tok, ok := e.InputTokens[m]; ok {
				a.totalInputTokens += tok
			}
			if tok, ok := e.OutputTokens[m]; ok {
				a.totalOutputTokens += tok
			}
			if tc, ok := e.ToolCalls[m]; ok {
				for _, count := range tc {
					a.totalToolCalls += count
				}
			}
			if pw, ok := e.ProposedWritesCount[m]; ok {
				a.totalProposedWrites += pw
			}
			switch {
			case e.Selected == m:
				a.wins++
			case e.Selected != "":
				// A different model was selected — this model lost this round.
				a.losses++
			case e.Rating == "bad":
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

