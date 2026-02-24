package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/diff"
	"github.com/suarezc/errata/internal/models"
)

var (
	addStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#00AF00"))
	removeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000"))
	contextStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	hunkStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#0087AF"))
	addHighlight    = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#00AF00"))
	removeHighlight = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")).Background(lipgloss.Color("#AF0000"))
)

// RenderDiffs returns a multi-line string showing diffs for all responses, including errors.
func RenderDiffs(responses []models.ModelResponse) string {
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000"))
	var sb strings.Builder
	for i, resp := range responses {
		color := colorFor(i)
		ruleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color))

		if !resp.OK() {
			sb.WriteString(ruleStyle.Render(fmt.Sprintf("── %s  %dms  error ", resp.ModelID, resp.LatencyMS)))
			sb.WriteByte('\n')
			sb.WriteString(errStyle.Render("  " + resp.Error))
			sb.WriteString("\n\n")
			continue
		}

		meta := fmt.Sprintf("%dms", resp.LatencyMS)
		if tot := resp.InputTokens + resp.OutputTokens; tot > 0 {
			meta += "  ·  " + fmtTokens(tot) + " tok"
			if resp.CostUSD > 0 {
				meta += fmt.Sprintf("  ·  $%.4f", resp.CostUSD)
			}
		}
		sb.WriteString(ruleStyle.Render(fmt.Sprintf("── %s  %s ", resp.ModelID, meta)))
		sb.WriteByte('\n')

		if len(resp.ProposedWrites) == 0 {
			sb.WriteString(contextStyle.Render("  (no file writes proposed)"))
			sb.WriteByte('\n')
			if resp.Text != "" {
				for _, line := range strings.Split(resp.Text, "\n") {
					sb.WriteString(contextStyle.Render("  " + line))
					sb.WriteByte('\n')
				}
			}
			sb.WriteByte('\n')
			continue
		}

		for _, fw := range resp.ProposedWrites {
			fd := diff.Compute(fw.Path, fw.Content)
			sb.WriteString(renderFileDiff(fd))
		}
		if resp.Text != "" {
			sb.WriteString(contextStyle.Render("  ── reasoning ──"))
			sb.WriteByte('\n')
			for _, line := range strings.Split(resp.Text, "\n") {
				if line != "" {
					sb.WriteString(contextStyle.Render("  " + line))
					sb.WriteByte('\n')
				}
			}
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
			if len(line.Spans) > 0 {
				sb.WriteString(addStyle.Render("    + " + string(line.Content[0])))
				for _, sp := range line.Spans {
					if sp.Changed {
						sb.WriteString(addHighlight.Render(sp.Text))
					} else {
						sb.WriteString(addStyle.Render(sp.Text))
					}
				}
			} else {
				sb.WriteString(addStyle.Render("    + " + line.Content))
			}
		case diff.Remove:
			if len(line.Spans) > 0 {
				sb.WriteString(removeStyle.Render("    - " + string(line.Content[0])))
				for _, sp := range line.Spans {
					if sp.Changed {
						sb.WriteString(removeHighlight.Render(sp.Text))
					} else {
						sb.WriteString(removeStyle.Render(sp.Text))
					}
				}
			} else {
				sb.WriteString(removeStyle.Render("    - " + line.Content))
			}
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
// Failed responses are shown as non-selectable; only OK responses get numbers.
func RenderSelectionMenu(responses []models.ModelResponse) string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Select a response to apply:"))
	sb.WriteByte('\n')

	selIdx := 0
	for _, resp := range responses {
		if !resp.OK() {
			sb.WriteString(dimStyle.Render(fmt.Sprintf("  -  %-30s (error)", resp.ModelID)))
			sb.WriteByte('\n')
			continue
		}
		selIdx++
		var files []string
		for _, fw := range resp.ProposedWrites {
			files = append(files, fw.Path)
		}
		fileStr := strings.Join(files, ", ")
		if fileStr == "" {
			fileStr = "(no writes)"
		}
		cost := ""
		if resp.CostUSD > 0 {
			cost = fmt.Sprintf("  $%.4f", resp.CostUSD)
		}
		line := fmt.Sprintf("  %d  %-30s (%dms%s)   →  %s",
			selIdx, resp.ModelID, resp.LatencyMS, cost, fileStr)
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteString("  s  Skip\n")
	return sb.String()
}
