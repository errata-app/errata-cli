package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
)

const maxPanelEvents = 20

// panelColors cycles through 6 distinct terminal colors.
var panelColors = []string{"#00AFAF", "#AF00AF", "#00AF00", "#AFAF00", "#0087AF", "#AF0000"}

func colorFor(idx int) string {
	return panelColors[idx%len(panelColors)]
}

// panelState holds the live state of one model's panel.
type panelState struct {
	modelID      string
	color        string
	events       []models.AgentEvent
	done         bool
	errMsg       string // non-empty when run errored
	latencyMS    int64
	inputTokens  int64
	outputTokens int64
	costUSD      float64
	histTokens   int64 // estimated history tokens at run start (for fill % display)
}

func newPanelState(modelID string, idx int) *panelState {
	return &panelState{modelID: modelID, color: colorFor(idx)}
}

func (p *panelState) addEvent(e models.AgentEvent) {
	p.events = append(p.events, e)
	if len(p.events) > maxPanelEvents {
		p.events = p.events[len(p.events)-maxPanelEvents:]
	}
}

// renderPanel returns a lipgloss-styled box for one model.
func renderPanel(p *panelState, width int) string {
	color := lipgloss.Color(p.color)
	if p.done && p.errMsg != "" {
		color = lipgloss.Color("#AF0000")
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(color)
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Width(width - 4).
		Padding(0, 1)

	status := "running…"
	if p.done {
		if p.errMsg != "" {
			short := p.errMsg
			if len(short) > 45 {
				short = short[:45] + "…"
			}
			status = "error: " + short
		} else {
			tok := fmtTokens(p.inputTokens + p.outputTokens)
			if p.costUSD > 0 {
				status = fmt.Sprintf("done  %dms  ·  %s tok  ·  $%.4f", p.latencyMS, tok, p.costUSD)
			} else {
				status = fmt.Sprintf("done  %dms  ·  %s tok", p.latencyMS, tok)
			}
			if cw := pricing.ContextWindowTokens(p.modelID); cw > 0 && p.histTokens > 0 {
				pct := float64(p.histTokens) / float64(cw) * 100
				status += fmt.Sprintf("  ·  ~%.0f%% ctx", pct)
			}
		}
	}
	title := titleStyle.Render(p.modelID) + "  " +
		lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#666666"}).Render(status)

	var lines []string
	lines = append(lines, title)
	for _, e := range p.events {
		lines = append(lines, renderEvent(e))
	}

	return borderStyle.Render(strings.Join(lines, "\n"))
}

// truncateLine strips after the first newline and caps at max runes.
func truncateLine(s string, max int) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		s = s[:idx]
	}
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

func renderEvent(e models.AgentEvent) string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	switch e.Type {
	case "reading":
		return dimStyle.Render("reading  ") + e.Data
	case "writing":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#AFAF00")).Render("writing  ") + e.Data
	case "bash":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#00AF87")).Render("bash     ") + truncateLine(e.Data, 60)
	case "error":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000")).Render("error    ") + truncateLine(e.Data, 60)
	case "text":
		return dimStyle.Render(truncateLine(e.Data, 60))
	default:
		return dimStyle.Render(e.Data)
	}
}

// fmtTokens formats a token count compactly: 1.2k, 34.5k, 1.2M.
func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// renderPanelRow lays panels side-by-side.
func renderPanelRow(panels []*panelState, termWidth int) string {
	if len(panels) == 0 {
		return ""
	}
	panelWidth := termWidth / len(panels)
	if panelWidth < 20 {
		panelWidth = 20
	}

	rendered := make([]string, len(panels))
	for i, p := range panels {
		rendered[i] = renderPanel(p, panelWidth)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}
