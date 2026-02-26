package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/models"
)

// ─── pure function tests ────────────────────────────────────────────────────

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		candidates []string
		want       string
	}{
		{nil, ""},
		{[]string{"abc"}, "abc"},
		{[]string{"abc", "abd"}, "ab"},
		{[]string{"claude-sonnet-4-6", "claude-opus-4-6"}, "claude-"},
		{[]string{"gpt-4o", "gemini-2.0"}, "g"},
		{[]string{"abc", "xyz"}, ""},
	}
	for _, tt := range tests {
		got := longestCommonPrefix(tt.candidates)
		if got != tt.want {
			t.Errorf("longestCommonPrefix(%v) = %q, want %q", tt.candidates, got, tt.want)
		}
	}
}

func TestCompleteArg(t *testing.T) {
	candidates := []string{"claude-sonnet-4-6", "claude-opus-4-6", "gpt-4o", "gemini-2.0-flash"}

	tests := []struct {
		partial  string
		wantRepl string
		wantOK   bool
	}{
		{"gpt", "gpt-4o ", true},                // unique match: complete + space
		{"claude", "claude-", true},              // multiple: complete to common prefix
		{"claude-s", "claude-sonnet-4-6 ", true}, // unique after longer partial
		{"xyz", "", false},                       // no match
		{"gem", "gemini-2.0-flash ", true},       // unique match
		{"g", "", false},                         // common prefix "g" not longer than "g"
	}
	for _, tt := range tests {
		gotRepl, gotOK := completeArg(tt.partial, candidates)
		if gotRepl != tt.wantRepl || gotOK != tt.wantOK {
			t.Errorf("completeArg(%q, ...) = (%q, %v), want (%q, %v)",
				tt.partial, gotRepl, gotOK, tt.wantRepl, tt.wantOK)
		}
	}
}

func TestCompleteArg_CaseInsensitive(t *testing.T) {
	candidates := []string{"claude-sonnet-4-6", "GPT-4o"}
	repl, ok := completeArg("gpt", candidates)
	if !ok || repl != "GPT-4o " {
		t.Errorf("expected case-insensitive match, got (%q, %v)", repl, ok)
	}
}

func TestCompleteArg_EmptyPartial(t *testing.T) {
	candidates := []string{"alpha", "beta"}
	// Empty partial matches all; two matches → common prefix.
	repl, ok := completeArg("", candidates)
	// "alpha" and "beta" share no common prefix beyond "", so no completion.
	if ok {
		t.Errorf("expected no completion for empty partial with divergent candidates, got (%q, %v)", repl, ok)
	}
}

func TestCompleteArg_EmptyPartialSingleCandidate(t *testing.T) {
	candidates := []string{"only-one"}
	repl, ok := completeArg("", candidates)
	if !ok || repl != "only-one " {
		t.Errorf("expected single candidate completion, got (%q, %v)", repl, ok)
	}
}

func TestLastWord(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"abc", "abc"},
		{"abc ", ""},
		{"abc def", "def"},
		{"abc def ", ""},
	}
	for _, tt := range tests {
		got := lastWord(tt.input)
		if got != tt.want {
			t.Errorf("lastWord(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── tryArgComplete integration tests ───────────────────────────────────────

func TestTryArgComplete_ModelSingleMatch(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"gpt-4o"}})
	result, ok := a.tryArgComplete("/model gpt")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "/model gpt-4o " {
		t.Errorf("got %q, want %q", result, "/model gpt-4o ")
	}
}

func TestTryArgComplete_ModelMultipleMatches(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"claude-opus-4-6"}})
	result, ok := a.tryArgComplete("/model claude")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "/model claude-" {
		t.Errorf("got %q, want %q", result, "/model claude-")
	}
}

func TestTryArgComplete_MultiWord(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"gpt-4o"}})
	result, ok := a.tryArgComplete("/model claude-sonnet-4-6 gpt")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "/model claude-sonnet-4-6 gpt-4o " {
		t.Errorf("got %q, want %q", result, "/model claude-sonnet-4-6 gpt-4o ")
	}
}

func TestTryArgComplete_ToolsOff(t *testing.T) {
	a := newAppForTest(t, nil)
	result, ok := a.tryArgComplete("/tools off ba")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "/tools off bash " {
		t.Errorf("got %q, want %q", result, "/tools off bash ")
	}
}

func TestTryArgComplete_ToolsOn(t *testing.T) {
	a := newAppForTest(t, nil)
	result, ok := a.tryArgComplete("/tools on rea")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "/tools on read_file " {
		t.Errorf("got %q, want %q", result, "/tools on read_file ")
	}
}

func TestTryArgComplete_NoMatch(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"gpt-4o"}})
	_, ok := a.tryArgComplete("/model xyz")
	if ok {
		t.Error("expected no completion for unknown prefix")
	}
}

func TestTryArgComplete_NotAnArgCommand(t *testing.T) {
	a := newAppForTest(t, nil)
	_, ok := a.tryArgComplete("/verbose")
	if ok {
		t.Error("expected no completion for /verbose")
	}
}

func TestTryArgComplete_BareModelNoArgs(t *testing.T) {
	// "/model" without trailing space should not trigger arg completion
	// (it should fall through to command-name completion instead).
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"gpt-4o"}})
	_, ok := a.tryArgComplete("/model")
	if ok {
		t.Error("expected no arg completion for bare /model without space")
	}
}

func TestTryArgComplete_SubsetSingleMatch(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"gpt-4o"}})
	result, ok := a.tryArgComplete("/subset gpt")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "/subset gpt-4o " {
		t.Errorf("got %q, want %q", result, "/subset gpt-4o ")
	}
}

func TestTryArgComplete_SubsetMultipleMatches(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"claude-opus-4-6"}})
	result, ok := a.tryArgComplete("/subset claude")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "/subset claude-" {
		t.Errorf("got %q, want %q", result, "/subset claude-")
	}
}

// ─── @mention completion tests ──────────────────────────────────────────────

func TestTryMentionComplete_SingleMatch(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"gpt-4o"}})
	result, ok := a.tryMentionComplete("@gpt")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "@gpt-4o " {
		t.Errorf("got %q, want %q", result, "@gpt-4o ")
	}
}

func TestTryMentionComplete_MultipleMatch(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"claude-opus-4-6"}})
	result, ok := a.tryMentionComplete("@claude")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "@claude-" {
		t.Errorf("got %q, want %q", result, "@claude-")
	}
}

func TestTryMentionComplete_NoMatch(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"gpt-4o"}})
	_, ok := a.tryMentionComplete("@xyz")
	if ok {
		t.Error("expected no completion for unknown prefix")
	}
}

func TestTryMentionComplete_MidSentence(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"claude-sonnet-4-6"}, uiStub{"gpt-4o"}})
	result, ok := a.tryMentionComplete("hello @gpt")
	if !ok {
		t.Fatal("expected completion")
	}
	if result != "hello @gpt-4o " {
		t.Errorf("got %q, want %q", result, "hello @gpt-4o ")
	}
}

func TestTryMentionComplete_NoBareAt(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"gpt-4o"}})
	_, ok := a.tryMentionComplete("@")
	if ok {
		t.Error("expected no completion for bare @")
	}
}

func TestTryMentionComplete_NotAtWord(t *testing.T) {
	a := newAppForTest(t, []models.ModelAdapter{uiStub{"gpt-4o"}})
	_, ok := a.tryMentionComplete("hello world")
	if ok {
		t.Error("expected no completion for non-@ word")
	}
}

// ─── hintWriter tests ───────────────────────────────────────────────────────

func TestHintWriter_CapsAtMaxHintLines(t *testing.T) {
	var sb strings.Builder
	plain := lipgloss.NewStyle()
	hw := newHintWriter(&sb, plain)

	// Add more items than maxHintLines.
	total := maxHintLines + 5
	for i := 0; i < total; i++ {
		hw.add("item")
	}
	hw.flush()

	out := sb.String()
	lines := strings.Split(strings.TrimPrefix(out, "\n"), "\n")

	// Should have maxHintLines content lines + 1 "... and N more" line.
	wantLines := maxHintLines + 1
	if len(lines) != wantLines {
		t.Errorf("got %d lines, want %d (max %d + 1 overflow)\noutput:\n%s",
			len(lines), wantLines, maxHintLines, out)
	}
	if !strings.Contains(out, "... and 5 more") {
		t.Errorf("expected '... and 5 more' notice, got:\n%s", out)
	}
}

func TestHintWriter_NoOverflowNoticeWhenUnderCap(t *testing.T) {
	var sb strings.Builder
	plain := lipgloss.NewStyle()
	hw := newHintWriter(&sb, plain)

	hw.add("a")
	hw.add("b")
	hw.flush()

	out := sb.String()
	if strings.Contains(out, "... and") {
		t.Errorf("should not show overflow notice when under cap, got:\n%s", out)
	}
}

func TestHintWriter_ExactlyAtCap(t *testing.T) {
	var sb strings.Builder
	plain := lipgloss.NewStyle()
	hw := newHintWriter(&sb, plain)

	for i := 0; i < maxHintLines; i++ {
		hw.add("item")
	}
	hw.flush()

	out := sb.String()
	if strings.Contains(out, "... and") {
		t.Errorf("should not show overflow notice when exactly at cap, got:\n%s", out)
	}
}

func TestHintWriter_ZeroItems(t *testing.T) {
	var sb strings.Builder
	plain := lipgloss.NewStyle()
	hw := newHintWriter(&sb, plain)
	hw.flush()

	if sb.Len() != 0 {
		t.Errorf("expected empty output for zero items, got %q", sb.String())
	}
}
