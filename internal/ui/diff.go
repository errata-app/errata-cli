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

// wrapLine breaks a long line into multiple lines at maxW runes each.
func wrapLine(s string, maxW int) []string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return []string{s}
	}
	var out []string
	for len(runes) > maxW {
		out = append(out, string(runes[:maxW]))
		runes = runes[maxW:]
	}
	out = append(out, string(runes))
	return out
}

// RenderDiffs returns a multi-line string showing diffs for all responses, including errors.
func RenderDiffs(responses []models.ModelResponse, width ...int) string {
	termW := 80
	if len(width) > 0 && width[0] > 0 {
		termW = width[0]
	}
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
			if resp.Text != "" {
				maxW := max(termW-2, 10) // 2 for "  " indent
				for line := range strings.SplitSeq(resp.Text, "\n") {
					for _, wl := range wrapLine(line, maxW) {
						sb.WriteString(contextStyle.Render("  " + wl))
						sb.WriteByte('\n')
					}
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
			maxW := max(termW-2, 10)
			for line := range strings.SplitSeq(resp.Text, "\n") {
				if line != "" {
					for _, wl := range wrapLine(line, maxW) {
						sb.WriteString(contextStyle.Render("  " + wl))
						sb.WriteByte('\n')
					}
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
		// line.Content includes a leading prefix character (+/-/ ) from
		// diff.Compute; strip it so the rendering prefix is not doubled.
		body := line.Content
		if len(body) > 0 {
			body = body[1:]
		}

		switch line.Kind {
		case diff.Add:
			if len(line.Spans) > 0 {
				sb.WriteString(addStyle.Render("    + "))
				for _, sp := range line.Spans {
					if sp.Changed {
						sb.WriteString(addHighlight.Render(sp.Text))
					} else {
						sb.WriteString(addStyle.Render(sp.Text))
					}
				}
			} else {
				sb.WriteString(addStyle.Render("    + " + body))
			}
		case diff.Remove:
			if len(line.Spans) > 0 {
				sb.WriteString(removeStyle.Render("    - "))
				for _, sp := range line.Spans {
					if sp.Changed {
						sb.WriteString(removeHighlight.Render(sp.Text))
					} else {
						sb.WriteString(removeStyle.Render(sp.Text))
					}
				}
			} else {
				sb.WriteString(removeStyle.Render("    - " + body))
			}
		case diff.Hunk:
			sb.WriteString(hunkStyle.Render("    " + line.Content))
		default:
			sb.WriteString(contextStyle.Render("      " + body))
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
	anyWrites := false
	for _, resp := range responses {
		if resp.OK() && len(resp.ProposedWrites) > 0 {
			anyWrites = true
			break
		}
	}

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	var sb strings.Builder

	header := "Select a response to apply:"
	if !anyWrites {
		header = "Vote for a response:"
	}
	sb.WriteString(lipgloss.NewStyle().Bold(true).Render(header))
	sb.WriteByte('\n')

	selIdx := 0
	for _, resp := range responses {
		if !resp.OK() {
			sb.WriteString(dimStyle.Render(fmt.Sprintf("  -  %-30s (error)", resp.ModelID)))
			sb.WriteByte('\n')
			continue
		}
		selIdx++
		cost := ""
		if resp.CostUSD > 0 {
			cost = fmt.Sprintf("  $%.4f", resp.CostUSD)
		}
		var line string
		if anyWrites {
			files := make([]string, 0, len(resp.ProposedWrites))
			for _, fw := range resp.ProposedWrites {
				files = append(files, fw.Path)
			}
			fileStr := strings.Join(files, ", ")
			if fileStr == "" {
				fileStr = "(no writes)"
			}
			line = fmt.Sprintf("  %d  %-30s (%dms%s)   →  %s",
				selIdx, resp.ModelID, resp.LatencyMS, cost, fileStr)
		} else {
			line = fmt.Sprintf("  %d  %-30s (%dms%s)",
				selIdx, resp.ModelID, resp.LatencyMS, cost)
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteString("  s  Skip\n")
	return sb.String()
}
