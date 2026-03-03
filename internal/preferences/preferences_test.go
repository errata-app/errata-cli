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
	"github.com/suarezc/errata/internal/tools"
)

func TestRecordAndLoadAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "claude-sonnet-4-6", LatencyMS: 100},
		{ModelID: "gpt-4o", LatencyMS: 200},
	}

	err := preferences.Record(path, "a test prompt", "claude-sonnet-4-6", "", "session1", responses)
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

	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", responses))
	require.NoError(t, preferences.Record(path, "p2", "b", "", "s2", responses))
	require.NoError(t, preferences.Record(path, "p3", "a", "", "s3", responses))

	tally := preferences.Summarize(path, nil)
	assert.Equal(t, 2, tally["a"])
	assert.Equal(t, 1, tally["b"])
}

func TestRecordAndLoadAll_LongPromptTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	err := preferences.Record(path, string(long), "m", "", "s", nil)
	require.NoError(t, err)

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Len(t, entries[0].PromptPreview, 120)
}

func TestRecord_PromptHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "write a sort function", "m", "", "s", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.NotEmpty(t, entries[0].PromptHash)
	assert.Contains(t, entries[0].PromptHash, "sha256:")
}

func TestRecord_UsesProvidedSessionID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p", "m", "", "my-session-id", nil))

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
	require.NoError(t, preferences.Record(path, "p", "a", "", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, int64(111), entries[0].LatenciesMS["a"])
	assert.Equal(t, int64(222), entries[0].LatenciesMS["b"])
}

func TestRecord_AppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "m", LatencyMS: 1}}

	require.NoError(t, preferences.Record(path, "first prompt", "m", "", "s1", responses))
	require.NoError(t, preferences.Record(path, "second prompt", "m", "", "s2", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 2)
	assert.Equal(t, "first prompt", entries[0].PromptPreview)
	assert.Equal(t, "second prompt", entries[1].PromptPreview)
}

func TestSummarize_EmptyWhenNoRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	tally := preferences.Summarize(path, nil)
	assert.Empty(t, tally)
}

// ─── SummarizeDetailed ────────────────────────────────────────────────────────

func TestSummarizeDetailed_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	stats := preferences.SummarizeDetailed(path, nil)
	assert.Empty(t, stats)
}

func TestSummarizeDetailed_WinsAndParticipations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	// a wins 2 of 3; b participates in all 3 and wins 1.
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}))
	require.NoError(t, preferences.Record(path, "p2", "b", "", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 150},
		{ModelID: "b", LatencyMS: 250},
	}))
	require.NoError(t, preferences.Record(path, "p3", "a", "", "s3", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 200},
		{ModelID: "b", LatencyMS: 300},
	}))

	stats := preferences.SummarizeDetailed(path, nil)
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
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
	}))
	require.NoError(t, preferences.Record(path, "p2", "a", "", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 300},
	}))

	stats := preferences.SummarizeDetailed(path, nil)
	assert.InDelta(t, 200.0, stats["a"].AvgLatencyMS, 0.1, "avg latency should be mean of 100 and 300")
}

func TestSummarizeDetailed_AvgCostUSD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100, CostUSD: 0.01},
	}))
	require.NoError(t, preferences.Record(path, "p2", "a", "", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100, CostUSD: 0.03},
	}))

	stats := preferences.SummarizeDetailed(path, nil)
	assert.InDelta(t, 0.02, stats["a"].AvgCostUSD, 1e-9, "avg cost should be mean of 0.01 and 0.03")
}

func TestSummarizeDetailed_ZeroCostForLegacyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100}, // CostUSD == 0 → not stored
	}))
	stats := preferences.SummarizeDetailed(path, nil)
	assert.Zero(t, stats["a"].AvgCostUSD)
}

func TestSummarizeDetailed_NoZeroDivide(t *testing.T) {
	// Every model in stats must have Participations >= 1; no division by zero.
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p", "a", "", "s", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 50},
	}))
	stats := preferences.SummarizeDetailed(path, nil)
	for _, s := range stats {
		assert.GreaterOrEqual(t, s.Participations, 1)
	}
}

// ─── RecordBad ────────────────────────────────────────────────────────────────

func TestRecordBad_StoresRatingField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.RecordBad(path, "a prompt", "a", "", "s1", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, "bad", entries[0].Rating)
	assert.Empty(t, entries[0].Selected)
	assert.Equal(t, []string{"a"}, entries[0].Models)
}

func TestRecordBad_CountedAsThumbsDown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.RecordBad(path, "p1", "a", "", "s1", responses))
	require.NoError(t, preferences.RecordBad(path, "p2", "a", "", "s2", responses))

	stats := preferences.SummarizeDetailed(path, nil)
	assert.Equal(t, 0, stats["a"].Wins)
	assert.Equal(t, 2, stats["a"].ThumbsDown)
	assert.Equal(t, 0, stats["a"].Losses)
	assert.InDelta(t, 100.0, stats["a"].BadRate, 0.1)
}

// ─── Losses ───────────────────────────────────────────────────────────────────

func TestSummarizeDetailed_Losses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	// a wins twice; b participates in all 3 and loses both times a wins, wins once.
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}))
	require.NoError(t, preferences.Record(path, "p2", "b", "", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 150},
		{ModelID: "b", LatencyMS: 250},
	}))
	require.NoError(t, preferences.Record(path, "p3", "a", "", "s3", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 200},
		{ModelID: "b", LatencyMS: 300},
	}))

	stats := preferences.SummarizeDetailed(path, nil)
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
		require.NoError(t, preferences.Record(path, fmt.Sprintf("p%d", i), "a", "", "s", []models.ModelResponse{
			{ModelID: "a", LatencyMS: 100},
			{ModelID: "b", LatencyMS: 200},
		}))
	}
	require.NoError(t, preferences.Record(path, "p3", "b", "", "s", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}))

	stats := preferences.SummarizeDetailed(path, nil)
	assert.Equal(t, 3, stats["b"].Losses)
	assert.InDelta(t, 75.0, stats["b"].LossRate, 0.1)
}

func TestSummarizeDetailed_MixedWinsLossesAndBad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses1 := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	responses2 := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}, {ModelID: "b", LatencyMS: 200}}

	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", responses2))    // a wins, b loses
	require.NoError(t, preferences.RecordBad(path, "p2", "a", "", "s2", responses1)) // a rated bad

	stats := preferences.SummarizeDetailed(path, nil)
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
	require.NoError(t, preferences.RecordBad(path, long, "a", "", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Len(t, entries[0].PromptPreview, 120)
}

// ─── Record/RecordBad: exactly 120 chars not truncated ──────────────────────

func TestRecord_Exactly120CharsNotTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	exact := strings.Repeat("z", 120)
	require.NoError(t, preferences.Record(path, exact, "m", "", "s", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, exact, entries[0].PromptPreview)
}

func TestRecord_StoresCostUSD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100, CostUSD: 0.0042},
		{ModelID: "b", LatencyMS: 200, CostUSD: 0.0},
	}
	require.NoError(t, preferences.Record(path, "p", "a", "", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.InDelta(t, 0.0042, entries[0].CostsUSD["a"], 1e-9)
	assert.Zero(t, entries[0].CostsUSD["b"])
}

// ─── RecordRewind & filterRewound ────────────────────────────────────────────

func TestRecordRewind_AppendsMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.RecordRewind(path, "test prompt", "s1"))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, "rewind", entries[0].Type)
	assert.Contains(t, entries[0].PromptHash, "sha256:")
	assert.Equal(t, "s1", entries[0].SessionID)
	assert.Empty(t, entries[0].Models)
	assert.Empty(t, entries[0].Selected)
}

func TestFilterRewound_SingleRewind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.Record(path, "prompt1", "a", "", "s1", responses))
	require.NoError(t, preferences.RecordRewind(path, "prompt1", "s1"))

	// Summarize should exclude the rewound entry.
	tally := preferences.Summarize(path, nil)
	assert.Empty(t, tally, "rewound entry should be excluded from tally")
}

func TestFilterRewound_MultipleRewinds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	// Two normal entries with same prompt, one rewind → only most recent excluded.
	require.NoError(t, preferences.Record(path, "prompt1", "a", "", "s1", responses))
	require.NoError(t, preferences.Record(path, "prompt1", "a", "", "s1", responses))
	require.NoError(t, preferences.RecordRewind(path, "prompt1", "s1"))

	tally := preferences.Summarize(path, nil)
	assert.Equal(t, 1, tally["a"], "only one of two entries should be excluded")
}

func TestFilterRewound_CrossSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	// Entry in session A.
	require.NoError(t, preferences.Record(path, "prompt1", "a", "", "sA", responses))
	// Rewind in session B with same prompt — should not affect session A.
	require.NoError(t, preferences.RecordRewind(path, "prompt1", "sB"))

	tally := preferences.Summarize(path, nil)
	assert.Equal(t, 1, tally["a"], "rewind in different session should not affect other sessions")
}

func TestSummarize_ExcludesRewound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	// a wins once, then that win is rewound.
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", responses))
	require.NoError(t, preferences.RecordRewind(path, "p1", "s1"))
	// b wins once (not rewound).
	require.NoError(t, preferences.Record(path, "p2", "b", "", "s1", []models.ModelResponse{{ModelID: "b", LatencyMS: 100}}))

	tally := preferences.Summarize(path, nil)
	assert.Equal(t, 0, tally["a"])
	assert.Equal(t, 1, tally["b"])
}

func TestSummarizeDetailed_ExcludesRewound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", responses))
	require.NoError(t, preferences.RecordRewind(path, "p1", "s1"))

	stats := preferences.SummarizeDetailed(path, nil)
	assert.Empty(t, stats, "all entries rewound → no stats")
}

// ─── ConfigHash ──────────────────────────────────────────────────────────────

func TestRecord_StoresConfigHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.Record(path, "p", "a", "sha256:abc123", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, "sha256:abc123", entries[0].ConfigHash)
}

func TestRecord_EmptyConfigHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p", "a", "", "s", nil))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Empty(t, entries[0].ConfigHash)
}

func TestSummarize_FilterByConfigHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.Record(path, "p1", "a", "sha256:cfg1", "s1", responses))
	require.NoError(t, preferences.Record(path, "p2", "b", "sha256:cfg2", "s2", []models.ModelResponse{{ModelID: "b", LatencyMS: 100}}))
	require.NoError(t, preferences.Record(path, "p3", "a", "sha256:cfg1", "s3", responses))

	// Unfiltered: a=2, b=1.
	tally := preferences.Summarize(path, nil)
	assert.Equal(t, 2, tally["a"])
	assert.Equal(t, 1, tally["b"])

	// Filtered by cfg1: a=2.
	filtered := preferences.Summarize(path, &preferences.StatsFilter{ConfigHash: "sha256:cfg1"})
	assert.Equal(t, 2, filtered["a"])
	assert.Equal(t, 0, filtered["b"])

	// Filtered by cfg2: b=1.
	filtered2 := preferences.Summarize(path, &preferences.StatsFilter{ConfigHash: "sha256:cfg2"})
	assert.Equal(t, 0, filtered2["a"])
	assert.Equal(t, 1, filtered2["b"])
}

func TestSummarizeDetailed_FilterByConfigHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p1", "a", "sha256:cfg1", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
		{ModelID: "b", LatencyMS: 200},
	}))
	require.NoError(t, preferences.Record(path, "p2", "b", "sha256:cfg2", "s2", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 150},
		{ModelID: "b", LatencyMS: 250},
	}))

	// Unfiltered: both present.
	all := preferences.SummarizeDetailed(path, nil)
	assert.Len(t, all, 2)

	// Filtered to cfg1 only.
	stats := preferences.SummarizeDetailed(path, &preferences.StatsFilter{ConfigHash: "sha256:cfg1"})
	assert.Equal(t, 1, stats["a"].Wins)
	assert.Equal(t, 1, stats["a"].Participations)
	assert.Equal(t, 0, stats["b"].Wins)
	assert.Equal(t, 1, stats["b"].Participations)
}

func TestBackwardCompat_NoConfigHash(t *testing.T) {
	// Simulate a legacy entry with no config_hash field.
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	content := `{"ts":"2026-01-01","prompt_hash":"sha256:abc","prompt_preview":"ok","models":["a"],"selected":"a","latencies_ms":{"a":100},"session_id":"s"}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Empty(t, entries[0].ConfigHash, "legacy entry should have empty config_hash")

	// Unfiltered should include it.
	tally := preferences.Summarize(path, nil)
	assert.Equal(t, 1, tally["a"])

	// Filtered by any hash should exclude it (empty != "sha256:xxx").
	filtered := preferences.Summarize(path, &preferences.StatsFilter{ConfigHash: "sha256:xxx"})
	assert.Empty(t, filtered)
}

func TestRecordBad_StoresConfigHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}
	require.NoError(t, preferences.RecordBad(path, "p", "a", "sha256:badcfg", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Equal(t, "sha256:badcfg", entries[0].ConfigHash)
	assert.Equal(t, "bad", entries[0].Rating)
}

func TestSummarize_FilterAndRewindInteraction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{{ModelID: "a", LatencyMS: 100}}

	// Two entries with cfg1, one rewound.
	require.NoError(t, preferences.Record(path, "p1", "a", "sha256:cfg1", "s1", responses))
	require.NoError(t, preferences.Record(path, "p1", "a", "sha256:cfg1", "s1", responses))
	require.NoError(t, preferences.RecordRewind(path, "p1", "s1"))

	// One entry with cfg2.
	require.NoError(t, preferences.Record(path, "p2", "b", "sha256:cfg2", "s2", []models.ModelResponse{{ModelID: "b", LatencyMS: 100}}))

	// Filtered by cfg1: only the non-rewound entry should count.
	filtered := preferences.Summarize(path, &preferences.StatsFilter{ConfigHash: "sha256:cfg1"})
	assert.Equal(t, 1, filtered["a"], "one of two cfg1 entries was rewound")
	assert.Equal(t, 0, filtered["b"], "b is in cfg2, should be excluded")

	// Filtered by cfg2: only b's entry.
	filtered2 := preferences.Summarize(path, &preferences.StatsFilter{ConfigHash: "sha256:cfg2"})
	assert.Equal(t, 0, filtered2["a"])
	assert.Equal(t, 1, filtered2["b"])
}

func TestSummarizeDetailed_EmptyFilterMatchesAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	require.NoError(t, preferences.Record(path, "p1", "a", "sha256:cfg1", "s1", []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100},
	}))
	require.NoError(t, preferences.Record(path, "p2", "b", "", "s2", []models.ModelResponse{
		{ModelID: "b", LatencyMS: 200},
	}))

	// Empty StatsFilter (ConfigHash=="") should match everything, same as nil.
	stats := preferences.SummarizeDetailed(path, &preferences.StatsFilter{})
	assert.Len(t, stats, 2)
	assert.Equal(t, 1, stats["a"].Wins)
	assert.Equal(t, 1, stats["b"].Wins)
}

// ─── New metrics: tokens, tool calls, proposed writes ─────────────────────

func TestRecord_NewMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{
			ModelID:      "a",
			LatencyMS:    100,
			InputTokens:  5200,
			OutputTokens: 1800,
			ToolCalls:    map[string]int{"read_file": 3, "write_file": 2},
			ProposedWrites: []tools.FileWrite{
				{Path: "f1.go", Content: "c1"},
				{Path: "f2.go", Content: "c2"},
			},
		},
		{
			ModelID:      "b",
			LatencyMS:    200,
			InputTokens:  6100,
			OutputTokens: 3200,
			ToolCalls:    map[string]int{"bash": 1},
			ProposedWrites: []tools.FileWrite{
				{Path: "f3.go", Content: "c3"},
			},
		},
	}
	require.NoError(t, preferences.Record(path, "p", "a", "", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	e := entries[0]

	// Input/output tokens.
	assert.Equal(t, int64(5200), e.InputTokens["a"])
	assert.Equal(t, int64(6100), e.InputTokens["b"])
	assert.Equal(t, int64(1800), e.OutputTokens["a"])
	assert.Equal(t, int64(3200), e.OutputTokens["b"])

	// Tool calls.
	require.Contains(t, e.ToolCalls, "a")
	assert.Equal(t, 3, e.ToolCalls["a"]["read_file"])
	assert.Equal(t, 2, e.ToolCalls["a"]["write_file"])
	require.Contains(t, e.ToolCalls, "b")
	assert.Equal(t, 1, e.ToolCalls["b"]["bash"])

	// Proposed writes count.
	assert.Equal(t, 2, e.ProposedWritesCount["a"])
	assert.Equal(t, 1, e.ProposedWritesCount["b"])
}

func TestRecord_ZeroMetricsStored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	responses := []models.ModelResponse{
		{ModelID: "a", LatencyMS: 100}, // zero tokens, no tool calls, no writes
	}
	require.NoError(t, preferences.Record(path, "p", "a", "", "s", responses))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Zero(t, e.InputTokens["a"])
	assert.Zero(t, e.OutputTokens["a"])
	assert.Nil(t, e.ToolCalls["a"])
	assert.Zero(t, e.ProposedWritesCount["a"])
}

func TestBackwardCompat_LegacyEntries(t *testing.T) {
	// Legacy entry without new fields should load fine.
	path := filepath.Join(t.TempDir(), "prefs.jsonl")
	content := `{"ts":"2026-01-01","prompt_hash":"sha256:abc","prompt_preview":"ok","models":["a"],"selected":"a","latencies_ms":{"a":100},"session_id":"s"}
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	entries := preferences.LoadAll(path)
	require.Len(t, entries, 1)
	assert.Nil(t, entries[0].InputTokens)
	assert.Nil(t, entries[0].OutputTokens)
	assert.Nil(t, entries[0].ToolCalls)
	assert.Nil(t, entries[0].ProposedWritesCount)
}

func TestSummarizeDetailed_NewAverages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.jsonl")

	// Entry 1: a has 1000 input, 500 output, 4 tool calls (read_file:3+bash:1), 2 writes.
	require.NoError(t, preferences.Record(path, "p1", "a", "", "s1", []models.ModelResponse{
		{
			ModelID:      "a",
			LatencyMS:    100,
			InputTokens:  1000,
			OutputTokens: 500,
			ToolCalls:    map[string]int{"read_file": 3, "bash": 1},
			ProposedWrites: []tools.FileWrite{
				{Path: "f1.go", Content: "c1"},
				{Path: "f2.go", Content: "c2"},
			},
		},
	}))

	// Entry 2: a has 3000 input, 1500 output, 6 tool calls (read_file:4+write_file:2), 0 writes.
	require.NoError(t, preferences.Record(path, "p2", "a", "", "s2", []models.ModelResponse{
		{
			ModelID:      "a",
			LatencyMS:    200,
			InputTokens:  3000,
			OutputTokens: 1500,
			ToolCalls:    map[string]int{"read_file": 4, "write_file": 2},
		},
	}))

	stats := preferences.SummarizeDetailed(path, nil)
	require.Contains(t, stats, "a")
	sa := stats["a"]
	assert.InDelta(t, 2000.0, sa.AvgInputTokens, 0.1)   // (1000+3000)/2
	assert.InDelta(t, 1000.0, sa.AvgOutputTokens, 0.1)   // (500+1500)/2
	assert.InDelta(t, 5.0, sa.AvgToolCalls, 0.1)         // (4+6)/2
	assert.InDelta(t, 1.0, sa.AvgProposedWrites, 0.1)    // (2+0)/2
}
