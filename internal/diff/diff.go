// Package diff computes unified-style diffs for proposed file writes.
// The FileDiff type is consumed by both the TUI and the future web server.
package diff

import (
	"os"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

const MaxDiffLines = 500

// LineKind classifies a single diff line.
type LineKind string

const (
	Add     LineKind = "add"
	Remove  LineKind = "remove"
	Context LineKind = "context"
	Hunk    LineKind = "hunk"
)

// WordSpan is a segment of a diff line with a flag indicating whether it was changed.
// Spans cover the text after the leading +/-/ prefix character in Content.
type WordSpan struct {
	Text    string `json:"text"`
	Changed bool   `json:"changed"`
}

// DiffLine is one line of a diff output.
type DiffLine struct {
	Kind    LineKind   `json:"kind"`
	Content string     `json:"content"`
	Spans   []WordSpan `json:"spans,omitempty"` // populated for matched Remove+Add pairs
}

// FileDiff is the result of diffing one proposed write against the on-disk file.
type FileDiff struct {
	Path      string
	IsNew     bool
	IsDeleted bool
	Adds      int
	Removes   int
	Lines     []DiffLine // capped at MaxDiffLines body lines
	Truncated int        // number of body lines omitted due to cap
}

// Compute diffs newContent against the file at path.
// If the file does not exist it is treated as empty (new file).
func Compute(path, newContent string) FileDiff {
	var oldContent string
	if data, err := os.ReadFile(path); err == nil {
		oldContent = string(data)
	}

	fd := FileDiff{
		Path:  path,
		IsNew: oldContent == "",
	}

	dmp := diffmatchpatch.New()
	// Line-level diff: convert lines to runes for efficient Myers diff,
	// then convert back to get per-line segments.
	oldRunes, newRunes, lineMap := dmp.DiffLinesToRunes(oldContent, newContent)
	diffs := dmp.DiffMainRunes(oldRunes, newRunes, false)
	diffs = dmp.DiffCharsToLines(diffs, lineMap)

	var bodyLines []DiffLine
	for _, d := range diffs {
		for _, line := range splitLines(d.Text) {
			switch d.Type {
			case diffmatchpatch.DiffInsert:
				fd.Adds++
				bodyLines = append(bodyLines, DiffLine{Kind: Add, Content: "+" + line})
			case diffmatchpatch.DiffDelete:
				fd.Removes++
				bodyLines = append(bodyLines, DiffLine{Kind: Remove, Content: "-" + line})
			case diffmatchpatch.DiffEqual:
				bodyLines = append(bodyLines, DiffLine{Kind: Context, Content: " " + line})
			}
		}
	}

	// Second pass: word-level highlighting for adjacent Remove+Add pairs.
	for i := 0; i < len(bodyLines)-1; i++ {
		if bodyLines[i].Kind == Remove && bodyLines[i+1].Kind == Add {
			oldText := bodyLines[i].Content[1:]   // strip leading '-'
			newText := bodyLines[i+1].Content[1:] // strip leading '+'
			wordDiffs := dmp.DiffMain(oldText, newText, false)
			wordDiffs = dmp.DiffCleanupSemantic(wordDiffs)
			bodyLines[i].Spans = makeSpans(wordDiffs, diffmatchpatch.DiffDelete)
			bodyLines[i+1].Spans = makeSpans(wordDiffs, diffmatchpatch.DiffInsert)
			i++ // skip the paired Add line
		}
	}

	if len(bodyLines) > MaxDiffLines {
		fd.Truncated = len(bodyLines) - MaxDiffLines
		fd.Lines = bodyLines[:MaxDiffLines]
	} else {
		fd.Lines = bodyLines
	}

	return fd
}

// makeSpans converts a character-level DiffMain result into WordSpans for one side.
// Equal segments appear unchanged on both sides; side's own segments are marked Changed.
func makeSpans(diffs []diffmatchpatch.Diff, side diffmatchpatch.Operation) []WordSpan {
	var spans []WordSpan
	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			spans = append(spans, WordSpan{Text: d.Text, Changed: false})
		case side:
			spans = append(spans, WordSpan{Text: d.Text, Changed: true})
		}
		// The other side's operations are omitted from this view.
	}
	return spans
}

// ComputeDeleted produces a diff for a file being deleted.
// It reads the file from disk and produces a diff where all lines are removals.
// If the file does not exist or cannot be read, it returns a minimal FileDiff.
func ComputeDeleted(path string) FileDiff {
	fd := FileDiff{
		Path:      path,
		IsDeleted: true,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fd
	}

	lines := splitLines(string(data))
	fd.Removes = len(lines)

	var bodyLines []DiffLine
	for _, line := range lines {
		bodyLines = append(bodyLines, DiffLine{Kind: Remove, Content: "-" + line})
	}

	if len(bodyLines) > MaxDiffLines {
		fd.Truncated = len(bodyLines) - MaxDiffLines
		fd.Lines = bodyLines[:MaxDiffLines]
	} else {
		fd.Lines = bodyLines
	}

	return fd
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Drop trailing empty element from a trailing newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
