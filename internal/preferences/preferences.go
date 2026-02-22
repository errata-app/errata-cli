// Package preferences manages the append-only JSONL preference log.
package preferences

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/suarezc/errata/internal/models"
)

// Entry is one recorded preference decision.
type Entry struct {
	TS            string         `json:"ts"`
	PromptHash    string         `json:"prompt_hash"`
	PromptPreview string         `json:"prompt_preview"`
	Models        []string       `json:"models"`
	Selected      string         `json:"selected"`
	LatenciesMS   map[string]int64 `json:"latencies_ms"`
	SessionID     string         `json:"session_id"`
}

// Record appends one preference entry to the JSONL log at path.
func Record(path, prompt, selectedModel, sessionID string, responses []models.ModelResponse) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	preview := prompt
	if len(preview) > 120 {
		preview = preview[:120]
	}

	hash := sha256.Sum256([]byte(prompt))

	modelIDs := make([]string, len(responses))
	latencies := make(map[string]int64, len(responses))
	for i, r := range responses {
		modelIDs[i] = r.ModelID
		latencies[r.ModelID] = r.LatencyMS
	}

	entry := Entry{
		TS:            time.Now().UTC().Format(time.RFC3339),
		PromptHash:    fmt.Sprintf("sha256:%x", hash),
		PromptPreview: preview,
		Models:        modelIDs,
		Selected:      selectedModel,
		LatenciesMS:   latencies,
		SessionID:     sessionID,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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
	for i, line := range splitLines(string(data)) {
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

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
