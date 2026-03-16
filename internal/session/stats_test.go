package session

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeRuns_BasicTally(t *testing.T) {
	runs := []RunSummary{
		{Models: []string{"m1", "m2"}, Selected: "m1"},
		{Models: []string{"m1", "m2"}, Selected: "m2"},
		{Models: []string{"m1", "m2"}, Selected: "m1"},
	}

	tally := SummarizeRuns(runs, nil)
	assert.Equal(t, 2, tally["m1"])
	assert.Equal(t, 1, tally["m2"])
}

func TestSummarizeRuns_ExcludesRewound(t *testing.T) {
	runs := []RunSummary{
		{PromptHash: "h1", Models: []string{"m1"}, Selected: "m1"},
		{PromptHash: "h1", Type: "rewind"},
	}

	tally := SummarizeRuns(runs, nil)
	assert.Empty(t, tally)
}

func TestSummarizeRuns_FilterByConfigHash(t *testing.T) {
	runs := []RunSummary{
		{Models: []string{"m1"}, Selected: "m1", ConfigHash: "abc"},
		{Models: []string{"m1"}, Selected: "m1", ConfigHash: "def"},
	}

	tally := SummarizeRuns(runs, &StatsFilter{ConfigHash: "abc"})
	assert.Equal(t, 1, tally["m1"])
}

func TestSummarizeRuns_EmptyRuns(t *testing.T) {
	tally := SummarizeRuns(nil, nil)
	assert.Empty(t, tally)
}

func TestSummarizeRunsDetailed_BasicStats(t *testing.T) {
	runs := []RunSummary{
		{
			Models:      []string{"m1", "m2"},
			Selected:    "m1",
			LatenciesMS: map[string]int64{"m1": 1000, "m2": 2000},
			CostsUSD:    map[string]float64{"m1": 0.01, "m2": 0.02},
			InputTokens: map[string]int64{"m1": 100, "m2": 200},
			OutputTokens: map[string]int64{"m1": 50, "m2": 80},
			ToolCalls:    map[string]map[string]int{"m1": {"read_file": 2}, "m2": {"bash": 1}},
			ProposedWritesCount: map[string]int{"m1": 1, "m2": 0},
		},
		{
			Models:      []string{"m1", "m2"},
			Selected:    "m2",
			LatenciesMS: map[string]int64{"m1": 3000, "m2": 1000},
			CostsUSD:    map[string]float64{"m1": 0.03, "m2": 0.01},
			InputTokens: map[string]int64{"m1": 300, "m2": 100},
			OutputTokens: map[string]int64{"m1": 150, "m2": 120},
			ToolCalls:    map[string]map[string]int{"m1": {"read_file": 4}, "m2": {"bash": 3}},
			ProposedWritesCount: map[string]int{"m1": 3, "m2": 2},
		},
	}

	stats := SummarizeRunsDetailed(runs, nil)

	m1 := stats["m1"]
	assert.Equal(t, 1, m1.Wins)
	assert.Equal(t, 1, m1.Losses)
	assert.Equal(t, 2, m1.Participations)
	assert.InDelta(t, 50.0, m1.WinRate, 0.01)
	assert.InDelta(t, 2000.0, m1.AvgLatencyMS, 0.01)
	assert.InDelta(t, 0.02, m1.AvgCostUSD, 0.001)
	assert.InDelta(t, 200.0, m1.AvgInputTokens, 0.01)
	assert.InDelta(t, 100.0, m1.AvgOutputTokens, 0.01)
	assert.InDelta(t, 3.0, m1.AvgToolCalls, 0.01) // (2+4)/2
	assert.InDelta(t, 2.0, m1.AvgProposedWrites, 0.01) // (1+3)/2

	m2 := stats["m2"]
	assert.Equal(t, 1, m2.Wins)
	assert.Equal(t, 1, m2.Losses)
}

func TestSummarizeRunsDetailed_ThumbsDown(t *testing.T) {
	runs := []RunSummary{
		{Models: []string{"m1"}, Rating: "bad"},
	}

	stats := SummarizeRunsDetailed(runs, nil)
	assert.Equal(t, 1, stats["m1"].ThumbsDown)
	assert.InDelta(t, 100.0, stats["m1"].BadRate, 0.01)
}

func TestSummarizeRunsDetailed_ExcludesRewound(t *testing.T) {
	runs := []RunSummary{
		{PromptHash: "h1", Models: []string{"m1"}, Selected: "m1"},
		{PromptHash: "h1", Type: "rewind"},
	}

	stats := SummarizeRunsDetailed(runs, nil)
	assert.Empty(t, stats)
}

func TestFilterRewound_MultipleRewinds(t *testing.T) {
	runs := []RunSummary{
		{PromptHash: "h1", Models: []string{"m1"}, Selected: "m1"},
		{PromptHash: "h1", Models: []string{"m1"}, Selected: "m1"},
		{PromptHash: "h1", Type: "rewind"},
		{PromptHash: "h1", Type: "rewind"},
	}

	result := filterRewound(runs)
	assert.Empty(t, result)
}

func TestFilterRewound_PreservesOrder(t *testing.T) {
	runs := []RunSummary{
		{PromptHash: "h1", Models: []string{"m1"}, Selected: "m1", PromptPreview: "first"},
		{PromptHash: "h2", Models: []string{"m1"}, Selected: "m1", PromptPreview: "second"},
		{PromptHash: "h1", Type: "rewind"},
	}

	result := filterRewound(runs)
	require.Len(t, result, 1)
	assert.Equal(t, "second", result[0].PromptPreview)
}

func TestSummarizeAcrossSessions(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	// Session 1
	sp1 := PathsFor(base, "ses_001")
	require.NoError(t, os.MkdirAll(sp1.Dir, 0o750))
	require.NoError(t, SaveMetadata(sp1.MetadataPath, SessionMetadata{
		ID: "ses_001", CreatedAt: now, LastActiveAt: now,
		Runs: []RunSummary{
			{Models: []string{"m1", "m2"}, Selected: "m1"},
		},
	}))

	// Session 2
	sp2 := PathsFor(base, "ses_002")
	require.NoError(t, os.MkdirAll(sp2.Dir, 0o750))
	require.NoError(t, SaveMetadata(sp2.MetadataPath, SessionMetadata{
		ID: "ses_002", CreatedAt: now, LastActiveAt: now,
		Runs: []RunSummary{
			{Models: []string{"m1", "m2"}, Selected: "m2"},
			{Models: []string{"m1", "m2"}, Selected: "m2"},
		},
	}))

	tally := SummarizeAcrossSessions(base, nil)
	assert.Equal(t, 1, tally["m1"])
	assert.Equal(t, 2, tally["m2"])
}

func TestSummarizeDetailedAcrossSessions(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	sp := PathsFor(base, "ses_001")
	require.NoError(t, os.MkdirAll(sp.Dir, 0o750))
	require.NoError(t, SaveMetadata(sp.MetadataPath, SessionMetadata{
		ID: "ses_001", CreatedAt: now, LastActiveAt: now,
		Runs: []RunSummary{
			{
				Models:      []string{"m1"},
				Selected:    "m1",
				LatenciesMS: map[string]int64{"m1": 500},
			},
		},
	}))

	stats := SummarizeDetailedAcrossSessions(base, nil)
	assert.Equal(t, 1, stats["m1"].Wins)
	assert.InDelta(t, 500.0, stats["m1"].AvgLatencyMS, 0.01)
}

func TestSummarizeAcrossSessions_FilterBySession(t *testing.T) {
	base := t.TempDir()
	now := time.Now()

	sp1 := PathsFor(base, "ses_001")
	require.NoError(t, os.MkdirAll(sp1.Dir, 0o750))
	require.NoError(t, SaveMetadata(sp1.MetadataPath, SessionMetadata{
		ID: "ses_001", CreatedAt: now, LastActiveAt: now,
		Runs: []RunSummary{{Models: []string{"m1"}, Selected: "m1"}},
	}))

	sp2 := PathsFor(base, "ses_002")
	require.NoError(t, os.MkdirAll(sp2.Dir, 0o750))
	require.NoError(t, SaveMetadata(sp2.MetadataPath, SessionMetadata{
		ID: "ses_002", CreatedAt: now, LastActiveAt: now,
		Runs: []RunSummary{{Models: []string{"m2"}, Selected: "m2"}},
	}))

	tally := SummarizeAcrossSessions(base, &StatsFilter{SessionID: "ses_001"})
	assert.Equal(t, 1, tally["m1"])
	assert.Equal(t, 0, tally["m2"])
}

func TestSummarizeAcrossSessions_EmptyDir(t *testing.T) {
	base := t.TempDir()
	tally := SummarizeAcrossSessions(base, nil)
	assert.Empty(t, tally)
}
