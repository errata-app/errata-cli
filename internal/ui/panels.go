package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/suarezc/errata/internal/models"
	"github.com/suarezc/errata/internal/pricing"
)

const maxPanelEvents = 20
const maxInlineEvents = 4

// panelColors cycles through 6 distinct terminal colors.
var panelColors = []string{"#00AFAF", "#AF00AF", "#00AF00", "#AFAF00", "#0087AF", "#AF0000"}

func colorFor(idx int) string {
	return panelColors[idx%len(panelColors)]
}

// panelState holds the live state of one model's panel.
type panelState struct {
	modelID         string
	color           string
	events          []models.AgentEvent
	done            bool
	errMsg          string // non-empty when run errored
	latencyMS       int64
	inputTokens     int64
	outputTokens    int64
	costUSD         float64
	histTokens      int64 // estimated history tokens at run start (for fill % display)

	toolUseCount int       // count of tool-use events (reading, writing, bash)
	expanded     bool      // when done: true = show events, false = collapsed summary
	startedAt    time.Time // creation time for live elapsed display
}

func newPanelState(modelID string, idx int) *panelState {
	return &panelState{modelID: modelID, color: colorFor(idx), startedAt: time.Now()}
}

func (p *panelState) addEvent(e models.AgentEvent) {
	switch e.Type {
	case models.EventReading, models.EventWriting, models.EventBash:
		p.toolUseCount++
	}
	p.events = append(p.events, e)
	if len(p.events) > maxPanelEvents {
		// Copy into a fresh slice so the old backing array can be GC'd.
		trimmed := make([]models.AgentEvent, maxPanelEvents)
		copy(trimmed, p.events[len(p.events)-maxPanelEvents:])
		p.events = trimmed
	}
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

// formatElapsed formats a duration in human-readable form.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	rem := s % 60
	if rem == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm %ds", m, rem)
}

// formatDoneSummary builds the parenthesized completion summary line.
func formatDoneSummary(p *panelState) string {
	var parts []string
	if p.toolUseCount > 0 {
		noun := "tool uses"
		if p.toolUseCount == 1 {
			noun = "tool use"
		}
		parts = append(parts, fmt.Sprintf("%d %s", p.toolUseCount, noun))
	}
	if tot := p.inputTokens + p.outputTokens; tot > 0 {
		parts = append(parts, fmtTokens(tot)+" tokens")
	}
	if p.latencyMS > 0 {
		parts = append(parts, formatElapsed(time.Duration(p.latencyMS)*time.Millisecond))
	}
	if p.costUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", p.costUSD))
	}
	if cw := pricing.ContextWindowTokens(p.modelID); cw > 0 && p.histTokens > 0 {
		pct := float64(p.histTokens) / float64(cw) * 100
		parts = append(parts, fmt.Sprintf("~%.0f%% ctx", pct))
	}
	if len(parts) == 0 {
		return "Done"
	}
	return "Done (" + strings.Join(parts, " · ") + ")"
}

// renderInlineEvent formats a single event for inline display.
func renderInlineEvent(e models.AgentEvent, termWidth int) string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	switch e.Type {
	case models.EventReading:
		return dimStyle.Render("reading ") + truncateLine(e.Data, max(termWidth-13, 20))
	case models.EventWriting:
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#AFAF00")).Render("writing ") + truncateLine(e.Data, max(termWidth-13, 20))
	case models.EventBash:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#00AF87")).Render("bash    ") + truncateLine(e.Data, max(termWidth-13, 20))
	case models.EventError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000")).Render("error   ") + truncateLine(e.Data, max(termWidth-13, 20))
	case models.EventText:
		return dimStyle.Render(truncateLine(e.Data, max(termWidth-5, 20)))
	default:
		return dimStyle.Render(truncateLine(e.Data, max(termWidth-5, 20)))
	}
}

// renderInlinePanel renders a single model panel in Claude Code inline style.
func renderInlinePanel(p *panelState, termWidth int) string {
	color := lipgloss.Color(p.color)
	if p.done && p.errMsg != "" {
		color = lipgloss.Color("#AF0000")
	}

	dotStyle := lipgloss.NewStyle().Foreground(color)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	modelStyle := lipgloss.NewStyle().Bold(true).Foreground(color)

	connector := dimStyle.Render("  ⎿  ")
	cont := "     "

	var sb strings.Builder

	// Header line: colored dot + model ID
	sb.WriteString(dotStyle.Render("⏺") + " " + modelStyle.Render(p.modelID))
	sb.WriteByte('\n')

	if p.done {
		if p.errMsg != "" {
			short := p.errMsg
			if len([]rune(short)) > 60 {
				short = string([]rune(short)[:60]) + "…"
			}
			sb.WriteString(connector)
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#AF0000")).
				Render("Error: " + short))
			sb.WriteByte('\n')
		} else if p.expanded {
			// Expanded view: full event list + summary
			for i, e := range p.events {
				if i == 0 {
					sb.WriteString(connector)
				} else {
					sb.WriteString(cont)
				}
				sb.WriteString(renderInlineEvent(e, termWidth))
				sb.WriteByte('\n')
			}
			if len(p.events) == 0 {
				sb.WriteString(connector)
			} else {
				sb.WriteString(cont)
			}
			sb.WriteString(dimStyle.Render(formatDoneSummary(p)))
			sb.WriteByte('\n')
		} else {
			// Collapsed view (default): summary line only
			sb.WriteString(connector)
			sb.WriteString(dimStyle.Render(formatDoneSummary(p)))
			sb.WriteByte('\n')
			sb.WriteString(cont)
			sb.WriteString(dimStyle.Render("(ctrl+o to expand)"))
			sb.WriteByte('\n')
		}
	} else {
		// Running: show last N events + running stats
		start := 0
		if len(p.events) > maxInlineEvents {
			start = len(p.events) - maxInlineEvents
		}
		visible := p.events[start:]
		for i, e := range visible {
			if i == 0 {
				sb.WriteString(connector)
			} else {
				sb.WriteString(cont)
			}
			sb.WriteString(renderInlineEvent(e, termWidth))
			sb.WriteByte('\n')
		}
		// Running stats line
		elapsed := time.Since(p.startedAt)
		if len(visible) == 0 {
			sb.WriteString(connector)
		} else {
			sb.WriteString(cont)
		}
		sb.WriteString(dimStyle.Render(
			fmt.Sprintf("%d tool uses · %s", p.toolUseCount, formatElapsed(elapsed))))
		sb.WriteByte('\n')
	}

	return sb.String()
}

// renderInlinePanels renders all panels vertically stacked.
func renderInlinePanels(panels []*panelState, termWidth int) string {
	if len(panels) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, p := range panels {
		sb.WriteString(renderInlinePanel(p, termWidth))
	}
	return sb.String()
}
