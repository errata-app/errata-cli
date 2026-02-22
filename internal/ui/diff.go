package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/diff"
	"github.com/suarezc/errata/internal/models"
)

var (
	addStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#00AF00"))
	removeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000"))
	contextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	hunkStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#0087AF"))
)

// RenderDiffs returns a multi-line string showing diffs for all successful responses.
func RenderDiffs(responses []models.ModelResponse) string {
	var sb strings.Builder
	for i, resp := range responses {
		if !resp.OK() {
			continue
		}
		color := colorFor(i)
		rule := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).
			Render(fmt.Sprintf("── %s  %dms ", resp.ModelID, resp.LatencyMS))
		sb.WriteString(rule)
		sb.WriteByte('\n')

		if len(resp.ProposedWrites) == 0 {
			sb.WriteString(contextStyle.Render("  (no file writes proposed)"))
			sb.WriteByte('\n')
			// show first line of text as a preview
			if resp.Text != "" {
				first := resp.Text
				if idx := strings.Index(first, "\n"); idx >= 0 {
					first = first[:idx]
				}
				if len(first) > 80 {
					first = first[:80] + "…"
				}
				sb.WriteString(contextStyle.Render("  " + first))
				sb.WriteByte('\n')
			}
			sb.WriteByte('\n')
			continue
		}

		for _, fw := range resp.ProposedWrites {
			fd := diff.Compute(fw.Path, fw.Content)
			sb.WriteString(renderFileDiff(fd))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func renderFileDiff(fd diff.FileDiff) string {
	var sb strings.Builder

	header := fmt.Sprintf("    %s", fd.Path)
	if fd.IsNew {
		header += "  (new file)"
	} else {
		header += fmt.Sprintf("  +%d -%d", fd.Adds, fd.Removes)
	}
	sb.WriteString(lipgloss.NewStyle().Bold(true).Render(header))
	sb.WriteByte('\n')

	for _, line := range fd.Lines {
		switch line.Kind {
		case diff.Add:
			sb.WriteString(addStyle.Render("    + " + line.Content))
		case diff.Remove:
			sb.WriteString(removeStyle.Render("    - " + line.Content))
		case diff.Hunk:
			sb.WriteString(hunkStyle.Render("    " + line.Content))
		default:
			sb.WriteString(contextStyle.Render("      " + line.Content))
		}
		sb.WriteByte('\n')
	}

	if fd.Truncated > 0 {
		sb.WriteString(contextStyle.Render(fmt.Sprintf("    … %d more lines", fd.Truncated)))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// RenderSelectionMenu returns the numbered selection prompt string.
func RenderSelectionMenu(responses []models.ModelResponse) string {
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Select a response to apply:"))
	sb.WriteByte('\n')

	for i, resp := range responses {
		if !resp.OK() {
			continue
		}
		var files []string
		for _, fw := range resp.ProposedWrites {
			files = append(files, fw.Path)
		}
		fileStr := strings.Join(files, ", ")
		if fileStr == "" {
			fileStr = "(no writes)"
		}
		line := fmt.Sprintf("  %d  %-30s (%dms)   →  %s",
			i+1, resp.ModelID, resp.LatencyMS, fileStr)
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteString("  s  Skip\n")
	return sb.String()
}
