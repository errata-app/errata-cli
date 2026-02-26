// Package checkpoint provides save/load for interrupted run state,
// enabling resume of partially-completed agent runs.
package checkpoint

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/tools"
)

// DefaultPath is the default location for checkpoint files.
const DefaultPath = "data/checkpoint.json"

// Checkpoint stores the state of an interrupted run.
type Checkpoint struct {
	Prompt     string             `json:"prompt"`
	Timestamp  time.Time          `json:"timestamp"`
	AdapterIDs []string           `json:"adapter_ids"`
	Responses  []ResponseSnapshot `json:"responses"`
	Verbose    bool               `json:"verbose"`
}

// ResponseSnapshot is a serializable snapshot of a ModelResponse.
type ResponseSnapshot struct {
	ModelID             string          `json:"model_id"`
	Text                string          `json:"text,omitempty"`
	LatencyMS           int64           `json:"latency_ms"`
	InputTokens         int64           `json:"input_tokens"`
	OutputTokens        int64           `json:"output_tokens"`
	CacheReadTokens     int64           `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64           `json:"cache_creation_tokens,omitempty"`
	CostUSD             float64         `json:"cost_usd"`
	ProposedWrites      []WriteSnapshot `json:"proposed_writes,omitempty"`
	Error               string          `json:"error,omitempty"`
	Interrupted         bool            `json:"interrupted"`
	Completed           bool            `json:"completed"`
}

// WriteSnapshot is a serializable FileWrite.
type WriteSnapshot struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// toWriteSnapshots converts FileWrite slices to WriteSnapshot slices.
func toWriteSnapshots(writes []tools.FileWrite) []WriteSnapshot {
	if len(writes) == 0 {
		return nil
	}
	out := make([]WriteSnapshot, len(writes))
	for i, w := range writes {
		out[i] = WriteSnapshot{Path: w.Path, Content: w.Content}
	}
	return out
}

// fromWriteSnapshots converts WriteSnapshot slices back to FileWrite slices.
func fromWriteSnapshots(snaps []WriteSnapshot) []tools.FileWrite {
	if len(snaps) == 0 {
		return nil
	}
	out := make([]tools.FileWrite, len(snaps))
	for i, s := range snaps {
		out[i] = tools.FileWrite{Path: s.Path, Content: s.Content}
	}
	return out
}

// FromModelResponse creates a snapshot from a ModelResponse.
func FromModelResponse(r models.ModelResponse) ResponseSnapshot {
	return ResponseSnapshot{
		ModelID:             r.ModelID,
		Text:                r.Text,
		LatencyMS:           r.LatencyMS,
		InputTokens:         r.InputTokens,
		OutputTokens:        r.OutputTokens,
		CacheReadTokens:     r.CacheReadTokens,
		CacheCreationTokens: r.CacheCreationTokens,
		CostUSD:             r.CostUSD,
		ProposedWrites:      toWriteSnapshots(r.ProposedWrites),
		Error:               r.Error,
		Interrupted:         r.Interrupted,
		Completed:           !r.Interrupted && r.Error == "",
	}
}

// ToModelResponse converts a snapshot back to a ModelResponse.
func (s ResponseSnapshot) ToModelResponse() models.ModelResponse {
	return models.ModelResponse{
		ModelID:             s.ModelID,
		Text:                s.Text,
		LatencyMS:           s.LatencyMS,
		InputTokens:         s.InputTokens,
		OutputTokens:        s.OutputTokens,
		CacheReadTokens:     s.CacheReadTokens,
		CacheCreationTokens: s.CacheCreationTokens,
		CostUSD:             s.CostUSD,
		ProposedWrites:      fromWriteSnapshots(s.ProposedWrites),
		Error:               s.Error,
		Interrupted:         s.Interrupted,
	}
}

// Build creates a Checkpoint from a completed (possibly interrupted) run.
// Returns nil if no responses are interrupted (no checkpoint needed).
func Build(prompt string, adapterIDs []string, responses []models.ModelResponse, verbose bool) *Checkpoint {
	hasInterrupted := false
	snapshots := make([]ResponseSnapshot, len(responses))
	for i, r := range responses {
		snapshots[i] = FromModelResponse(r)
		if r.Interrupted {
			hasInterrupted = true
		}
	}
	if !hasInterrupted {
		return nil
	}
	return &Checkpoint{
		Prompt:     prompt,
		Timestamp:  time.Now(),
		AdapterIDs: adapterIDs,
		Responses:  snapshots,
		Verbose:    verbose,
	}
}

// Save atomically writes a checkpoint to path.
func Save(path string, cp Checkpoint) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a checkpoint from path.
// Returns (nil, nil) if no checkpoint file exists.
func Load(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		log.Printf("checkpoint: corrupt file %q: %v", path, err)
		return nil, err
	}
	return &cp, nil
}

// Clear deletes the checkpoint file. Returns nil if the file does not exist.
func Clear(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ─── Incremental saver ──────────────────────────────────────────────────────

// IncrementalSaver aggregates per-model snapshots from concurrent adapter
// goroutines and writes them to disk atomically after each update. This ensures
// that the checkpoint file reflects the most recent turn boundary even if the
// process is killed ungracefully (SIGKILL, OOM, power loss).
//
// All methods are safe for concurrent use.
type IncrementalSaver struct {
	mu         sync.Mutex
	path       string
	prompt     string
	verbose    bool
	adapterIDs []string
	snapshots  map[string]ResponseSnapshot
}

// NewIncrementalSaver creates a saver that writes checkpoints to path.
func NewIncrementalSaver(path, prompt string, adapterIDs []string, verbose bool) *IncrementalSaver {
	return &IncrementalSaver{
		path:       path,
		prompt:     prompt,
		verbose:    verbose,
		adapterIDs: adapterIDs,
		snapshots:  make(map[string]ResponseSnapshot),
	}
}

// Update stores the latest snapshot for a model and flushes to disk.
func (s *IncrementalSaver) Update(modelID string, snap ResponseSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[modelID] = snap
	s.flush()
}

// MarkCompleted stores the final snapshot for a model with Completed=true and flushes.
func (s *IncrementalSaver) MarkCompleted(modelID string, snap ResponseSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap.Completed = true
	s.snapshots[modelID] = snap
	s.flush()
}

// flush writes the current state to disk. Caller must hold s.mu.
func (s *IncrementalSaver) flush() {
	responses := make([]ResponseSnapshot, 0, len(s.adapterIDs))
	for _, id := range s.adapterIDs {
		if snap, ok := s.snapshots[id]; ok {
			responses = append(responses, snap)
		}
	}
	if len(responses) == 0 {
		return
	}
	cp := Checkpoint{
		Prompt:     s.prompt,
		Timestamp:  time.Now(),
		AdapterIDs: s.adapterIDs,
		Responses:  responses,
		Verbose:    s.verbose,
	}
	_ = Save(s.path, cp) // best-effort; never crash the run
}

// SnapshotFromPartial converts a PartialSnapshot (emitted by adapters via
// AgentEvent) into a ResponseSnapshot suitable for incremental checkpointing.
func SnapshotFromPartial(modelID string, ps models.PartialSnapshot) ResponseSnapshot {
	return ResponseSnapshot{
		ModelID:        modelID,
		Text:           ps.Text,
		InputTokens:    ps.InputTokens,
		OutputTokens:   ps.OutputTokens,
		CostUSD:        ps.CostUSD,
		LatencyMS:      ps.LatencyMS,
		ProposedWrites: toWriteSnapshots(ps.Writes),
		Interrupted:    true, // still in progress
	}
}
