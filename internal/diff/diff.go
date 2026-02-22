// Package diff computes unified-style diffs for proposed file writes.
// The FileDiff type is consumed by both the TUI and the future web server.
package diff

import (
	"os"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

const MaxDiffLines = 20

// LineKind classifies a single diff line.
type LineKind string

const (
	Add     LineKind = "add"
	Remove  LineKind = "remove"
	Context LineKind = "context"
	Hunk    LineKind = "hunk"
)

// DiffLine is one line of a diff output.
type DiffLine struct {
	Kind    LineKind
	Content string
}

// FileDiff is the result of diffing one proposed write against the on-disk file.
type FileDiff struct {
	Path      string
	IsNew     bool
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
