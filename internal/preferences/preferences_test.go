package preferences_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/preferences"
)

func TestRecordAndLoadAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "claude-sonnet-4-6", LatencyMS: 100},
		{ModelID: "gpt-4o", LatencyMS: 200},
	}

	err := preferences.Record(path, "a test prompt", "claude-sonnet-4-6", "session1", responses)
	require.NoError(t, err)

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Equal(t, "claude-sonnet-4-6", e.Selected)
	assert.Equal(t, "session1", e.SessionID)
	assert.Contains(t, e.PromptPreview, "a test prompt")
	assert.Len(t, e.Models, 2)
	assert.Equal(t, int64(100), e.LatenciesMS["claude-sonnet-4-6"])
}

func TestLoadAll_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	entries := preferences.LoadAll(path)
	assert.Nil(t, entries)
}

func TestSummarize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 1}}

	require.NoError(t, preferences.Record(path, "p1", "a", "s1", responses))
	require.NoError(t, preferences.Record(path, "p2", "b", "s2", responses))
	require.NoError(t, preferences.Record(path, "p3", "a", "s3", responses))

	tally := preferences.Summarize(path)
	assert.Equal(t, 2, tally["a"])
	assert.Equal(t, 1, tally["b"])
}

func TestRecordAndLoadAll_LongPromptTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	err := preferences.Record(path, string(long), "m", "s", nil)
	require.NoError(t, err)

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, 120, len(entries[0].PromptPreview))
}

func TestRecord_PromptHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "write a sort function", "m", "s", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.True(t, len(entries[0].PromptHash) > 0)
	assert.Contains(t, entries[0].PromptHash, "sha256:")
}

func TestRecord_UsesProvidedSessionID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p", "m", "my-session-id", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, "my-session-id", entries[0].SessionID)
}

func TestRecord_LatenciesMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "a", LatencyMS: 111},
		{ModelID: "b", LatencyMS: 222},
	}
	require.NoError(t, preferences.Record(path, "p", "a", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, int64(111), entries[0].LatenciesMS["a"])
	assert.Equal(t, int64(222), entries[0].LatenciesMS["b"])
}

func TestRecord_AppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "m", LatencyMS: 1}}

	require.NoError(t, preferences.Record(path, "first prompt", "m", "s1", responses))
	require.NoError(t, preferences.Record(path, "second prompt", "m", "s2", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 2)
	assert.Equal(t, "first prompt", entries[0].PromptPreview)
	assert.Equal(t, "second prompt", entries[1].PromptPreview)
}

func TestSummarize_EmptyWhenNoRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	tally := preferences.Summarize(path)
	assert.Empty(t, tally)
}

// ─── SummarizeDetailed ────────────────────────────────────────────────────────

func TestSummarizeDetailed_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	stats := preferences.SummarizeDetailed(path)
	assert.Empty(t, stats)
}

func TestSummarizeDetailed_WinsAndParticipations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	// a wins 2 of 3; b participates in all 3 and wins 1.
	require.NoError(t, preferences.Record(path, "p1", "a", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}))
	require.NoError(t, preferences.Record(path, "p2", "b", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 150},
		{ModelID: "b", LatencyMS: 250},
	}))
	require.NoError(t, preferences.Record(path, "p3", "a", "s3", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 200},
		{ModelID: "b", LatencyMS: 300},
	}))

	stats := preferences.SummarizeDetailed(path)
	require.Contains(t, stats, "a")
	require.Contains(t, stats, "b")

	sa := stats["a"]
	assert.Equal(t, 2, sa.Wins)
	assert.Equal(t, 3, sa.Participations)
	assert.InDelta(t, 66.67, sa.WinRate, 0.1)

	sb := stats["b"]
	assert.Equal(t, 1, sb.Wins)
	assert.Equal(t, 3, sb.Participations)
	assert.InDelta(t, 33.33, sb.WinRate, 0.1)
}

func TestSummarizeDetailed_AvgLatency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p1", "a", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
	}))
	require.NoError(t, preferences.Record(path, "p2", "a", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 300},
	}))

	stats := preferences.SummarizeDetailed(path)
	assert.InDelta(t, 200.0, stats["a"].AvgLatencyMS, 0.1, "avg latency should be mean of 100 and 300")
}

func TestSummarizeDetailed_AvgCostUSD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p1", "a", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100, CostUSD: 0.01},
	}))
	require.NoError(t, preferences.Record(path, "p2", "a", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100, CostUSD: 0.03},
	}))

	stats := preferences.SummarizeDetailed(path)
	assert.InDelta(t, 0.02, stats["a"].AvgCostUSD, 1e-9, "avg cost should be mean of 0.01 and 0.03")
}

func TestSummarizeDetailed_ZeroCostForLegacyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p1", "a", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100}, // CostUSD == 0 → not stored
	}))
	stats := preferences.SummarizeDetailed(path)
	assert.Equal(t, 0.0, stats["a"].AvgCostUSD)
}

func TestSummarizeDetailed_NoZeroDivide(t *testing.T) {
	// Every model in stats must have Participations >= 1; no division by zero.
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p", "a", "s", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 50},
	}))
	stats := preferences.SummarizeDetailed(path)
	for _, s := range stats {
		assert.GreaterOrEqual(t, s.Participations, 1)
	}
}

// ─── RecordBad ────────────────────────────────────────────────────────────────

func TestRecordBad_StoresRatingField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.RecordBad(path, "a prompt", "a", "s1", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, "bad", entries[0].Rating)
	assert.Equal(t, "", entries[0].Selected)
	assert.Equal(t, []string{"a"}, entries[0].Models)
}

func TestRecordBad_CountedAsThumbsDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.RecordBad(path, "p1", "a", "s1", responses))
	require.NoError(t, preferences.RecordBad(path, "p2", "a", "s2", responses))

	stats := preferences.SummarizeDetailed(path)
	assert.Equal(t, 0, stats["a"].Wins)
	assert.Equal(t, 2, stats["a"].ThumbsDown)
	assert.Equal(t, 0, stats["a"].Losses)
	assert.InDelta(t, 100.0, stats["a"].BadRate, 0.1)
}

// ─── Losses ───────────────────────────────────────────────────────────────────

func TestSummarizeDetailed_Losses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	// a wins twice; b participates in all 3 and loses both times a wins, wins once.
	require.NoError(t, preferences.Record(path, "p1", "a", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}))
	require.NoError(t, preferences.Record(path, "p2", "b", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 150},
		{ModelID: "b", LatencyMS: 250},
	}))
	require.NoError(t, preferences.Record(path, "p3", "a", "s3", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 200},
		{ModelID: "b", LatencyMS: 300},
	}))

	stats := preferences.SummarizeDetailed(path)
	// a: 2 wins, 1 loss (p2 where b won)
	assert.Equal(t, 2, stats["a"].Wins)
	assert.Equal(t, 1, stats["a"].Losses)
	assert.Equal(t, 0, stats["a"].ThumbsDown)
	// b: 1 win, 2 losses (p1 and p3 where a won)
	assert.Equal(t, 1, stats["b"].Wins)
	assert.Equal(t, 2, stats["b"].Losses)
	assert.Equal(t, 0, stats["b"].ThumbsDown)
}

func TestSummarizeDetailed_LossRateCalculation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	// b participates 4 times, loses 3 of them.
	for i := range 3 {
		require.NoError(t, preferences.Record(path, fmt.Sprintf("p%d", i), "a", "s", []models.ModelResponse{
			{ModelID: "a", LatencyMS: 100},
			{ModelID: "b", LatencyMS: 200},
		}))
	}
	require.NoError(t, preferences.Record(path, "p3", "b", "s", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}))

	stats := preferences.SummarizeDetailed(path)
	assert.Equal(t, 3, stats["b"].Losses)
	assert.InDelta(t, 75.0, stats["b"].LossRate, 0.1)
}

func TestSummarizeDetailed_MixedWinsLossesAndBad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses1 := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	responses2 := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}, {ModelID: "b", LatencyMS: 200}}

	require.NoError(t, preferences.Record(path, "p1", "a", "s1", responses2))    // a wins, b loses
	require.NoError(t, preferences.RecordBad(path, "p2", "a", "s2", responses1)) // a rated bad

	stats := preferences.SummarizeDetailed(path)
	assert.Equal(t, 1, stats["a"].Wins)
	assert.Equal(t, 0, stats["a"].Losses)
	assert.Equal(t, 1, stats["a"].ThumbsDown)
	assert.Equal(t, 0, stats["b"].Wins)
	assert.Equal(t, 1, stats["b"].Losses)
	assert.Equal(t, 0, stats["b"].ThumbsDown)
}

// ─── LoadAll edge cases ─────────────────────────────────────────────────────

func TestLoadAll_CorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	content := `{"ts":"2026-01-01","prompt_hash":"sha256:abc","prompt_preview":"ok","models":["a"],"selected":"a","latencies_ms":{"a":100},"session_id":"s"}
{bad json here}
{"ts":"2026-01-02","prompt_hash":"sha256:def","prompt_preview":"ok2","models":["b"],"selected":"b","latencies_ms":{"b":200},"session_id":"s2"}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	entries := preferences.LoadAll(path)
	assert.Len(t, entries, 2, "corrupt line should be skipped, valid lines retained")
}

func TestLoadAll_AllCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{bad}\n{also bad}\n"), 0o644))
	entries := preferences.LoadAll(path)
	assert.Empty(t, entries)
}

// ─── RecordBad prompt truncation ────────────────────────────────────────────

func TestRecordBad_LongPromptTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	long := strings.Repeat("y", 200)
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.RecordBad(path, long, "a", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, 120, len(entries[0].PromptPreview))
}

// ─── Record/RecordBad: exactly 120 chars not truncated ──────────────────────

func TestRecord_Exactly120CharsNotTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	exact := strings.Repeat("z", 120)
	require.NoError(t, preferences.Record(path, exact, "m", "s", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, exact, entries[0].PromptPreview)
}

func TestRecord_StoresCostUSD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100, CostUSD: 0.0042},
		{ModelID: "b", LatencyMS: 200, CostUSD: 0.0}, // zero cost → omitted
	}
	require.NoError(t, preferences.Record(path, "p", "a", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.InDelta(t, 0.0042, entries[0].CostsUSD["a"], 1e-9)
	_, hasB := entries[0].CostsUSD["b"]
	assert.False(t, hasB, "zero-cost model should not be stored in CostsUSD")
}
