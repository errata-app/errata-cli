package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// wrapText word-wraps text to fit within width columns, prepends indent spaces
// to every line (first and continuation), and applies style to each line.
// Returns the full block with no trailing newline — caller adds it.
// When width <= 0, returns style.Render(indent + text) unchanged (no wrapping).
func wrapText(text string, width, indent int, style lipgloss.Style) string { //nolint:gocritic // lipgloss.Style is idiomatically passed by value
	pad := strings.Repeat(" ", indent)
	if width <= 0 {
		return style.Render(pad + text)
	}

	contentWidth := max(width-indent, 1)

	var lines []string
	for paragraph := range strings.SplitSeq(text, "\n") {
		wrapped := ansi.Wordwrap(paragraph, contentWidth, "")
		for line := range strings.SplitSeq(wrapped, "\n") {
			lines = append(lines, style.Render(pad+line))
		}
	}

	return strings.Join(lines, "\n")
}
