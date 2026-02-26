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
	TS            string             `json:"ts"`
	PromptHash    string             `json:"prompt_hash"`
	PromptPreview string             `json:"prompt_preview"`
	Models        []string           `json:"models"`
	Selected      string             `json:"selected"`
	Rating        string             `json:"rating,omitempty"` // "bad" for single-model thumbs-down; empty otherwise
	LatenciesMS   map[string]int64   `json:"latencies_ms"`
	CostsUSD      map[string]float64 `json:"costs_usd,omitempty"`
	SessionID     string             `json:"session_id"`
}

// Record appends one preference entry to the JSONL log at path.
func Record(path, prompt, selectedModel, sessionID string, responses []models.ModelResponse) error {
	return recordEntry(path, prompt, selectedModel, "", sessionID, responses)
}

// RecordBad appends a thumbs-down entry for a single-model response.
// Selected is left empty; Rating is set to "bad".
func RecordBad(path, prompt, modelID, sessionID string, responses []models.ModelResponse) error {
	return recordEntry(path, prompt, "", "bad", sessionID, responses)
}

// recordEntry is the shared implementation for Record and RecordBad.
func recordEntry(path, prompt, selected, rating, sessionID string, responses []models.ModelResponse) error {
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
	for i, r := range responses {
		modelIDs[i] = r.ModelID
		latencies[r.ModelID] = r.LatencyMS
		if r.CostUSD > 0 {
			costs[r.ModelID] = r.CostUSD
		}
	}
	if len(costs) == 0 {
		costs = nil
	}

	entry := Entry{
		TS:            time.Now().UTC().Format(time.RFC3339),
		PromptHash:    fmt.Sprintf("sha256:%x", hash),
		PromptPreview: preview,
		Models:        modelIDs,
		Selected:      selected,
		Rating:        rating,
		LatenciesMS:   latencies,
		CostsUSD:      costs,
		SessionID:     sessionID,
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
func Summarize(path string) map[string]int {
	tally := map[string]int{}
	for _, e := range LoadAll(path) {
		if e.Selected != "" {
			tally[e.Selected]++
		}
	}
	return tally
}

// ModelStats holds detailed per-model analytics derived from preference entries.
type ModelStats struct {
	Wins           int
	Losses         int     // times model was in a run where a different model was selected
	ThumbsDown     int     // times model received a single-model thumbs-down rating
	Participations int     // times model appeared in any recorded entry
	WinRate        float64 // Wins / Participations × 100; 0 if Participations == 0
	LossRate       float64 // Losses / Participations × 100
	BadRate        float64 // ThumbsDown / Participations × 100
	AvgLatencyMS   float64 // mean latency across all runs where model participated
	AvgCostUSD     float64 // mean cost; 0 for entries recorded before cost tracking
}

// SummarizeDetailed computes per-model analytics from all preference entries.
// Entries recorded before cost tracking was added have zero cost contribution.
func SummarizeDetailed(path string) map[string]ModelStats {
	type accumulator struct {
		wins           int
		losses         int
		thumbsDown     int
		participations int
		totalLatencyMS int64
		totalCostUSD   float64
	}

	acc := map[string]*accumulator{}
	for _, e := range LoadAll(path) {
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
		if a.participations > 0 {
			winRate = float64(a.wins) / float64(a.participations) * 100
			lossRate = float64(a.losses) / float64(a.participations) * 100
			badRate = float64(a.thumbsDown) / float64(a.participations) * 100
			avgLatency = float64(a.totalLatencyMS) / float64(a.participations)
			avgCost = a.totalCostUSD / float64(a.participations)
		}
		result[m] = ModelStats{
			Wins:           a.wins,
			Losses:         a.losses,
			ThumbsDown:     a.thumbsDown,
			Participations: a.participations,
			WinRate:        winRate,
			LossRate:       lossRate,
			BadRate:        badRate,
			AvgLatencyMS:   avgLatency,
			AvgCostUSD:     avgCost,
		}
	}
	return result
}

