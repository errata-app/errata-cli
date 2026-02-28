package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// handleSidebarKey processes keys when the sidebar has focus.
// Plain j/k scroll, 1-9 toggle sections, Escape returns to input.
func (a App) handleSidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) { //nolint:gocritic // bubbletea tea.Model requires value receiver
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlCloseBracket:
		a.sidebarFocused = false
		return a.withFeedRebuilt(false), nil
	case tea.KeyRunes:
		r := string(msg.Runes)
		switch r {
		case "j":
			a.sidebarScrollY++
			return a.withFeedRebuilt(false), nil
		case "k":
			if a.sidebarScrollY > 0 {
				a.sidebarScrollY--
			}
			return a.withFeedRebuilt(false), nil
		default:
			if len(r) == 1 && r[0] >= '1' && r[0] <= '9' {
				idx := int(r[0] - '1')
				if idx < len(a.sidebarSections) {
					if a.sidebarExpandedSet == nil {
						a.sidebarExpandedSet = make(map[int]bool)
					}
					a.sidebarExpandedSet[idx] = !a.sidebarExpandedSet[idx]
					return a.withFeedRebuilt(false), nil
				}
			}
		}
	case tea.KeyDown:
		a.sidebarScrollY++
		return a.withFeedRebuilt(false), nil
	case tea.KeyUp:
		if a.sidebarScrollY > 0 {
			a.sidebarScrollY--
		}
		return a.withFeedRebuilt(false), nil
	}
	return a, nil
}

// rebuildSidebar refreshes the sidebar section data from the current recipe.
func (a *App) rebuildSidebar() {
	rec := a.sessionRecipe
	if rec == nil {
		rec = a.recipe
	}
	a.sidebarSections = buildConfigSections(rec, a.adapters, a.disabledTools)
}

// renderSidebar renders the pinned config sidebar as a fixed-height string.
// It is a pure function: all state is passed in, making it easy to test.
func renderSidebar(sections []configSection, expandedSet map[int]bool, scrollY, width, height int, modified, focused bool) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#00AFAF"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF"))

	var lines []string

	// Title line.
	title := " Config"
	if modified {
		title += " [mod]"
	}
	lines = append(lines, truncateSidebarLine(titleStyle.Render(title), width))

	// Separator.
	sep := strings.Repeat("\u2500", width)
	lines = append(lines, dimStyle.Render(sep))

	// Sections.
	for i, sec := range sections {
		if expandedSet[i] {
			// Expanded: show section name + summary lines + detail.
			header := fmt.Sprintf("\u25be %s", sec.Name)
			lines = append(lines, truncateSidebarLine(nameStyle.Render(header), width))
			lines = append(lines, truncateSidebarLine(dimStyle.Render("  "+sec.Summary), width))
			if sec.DetailDesc != "" {
				for _, wl := range wrapSidebarText(sec.DetailDesc, width-2) {
					lines = append(lines, truncateSidebarLine(dimStyle.Render("  "+wl), width))
				}
			}
		} else {
			// Collapsed: one-line summary.
			line := fmt.Sprintf("\u25b8 %s  %s", sec.Name, sec.Summary)
			lines = append(lines, truncateSidebarLine(dimStyle.Render(line), width))
		}
	}

	// Footer hint.
	lines = append(lines, "")
	if focused {
		focusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AFAF"))
		lines = append(lines, truncateSidebarLine(focusStyle.Render("1-9 expand  j/k scroll  Esc back"), width))
	} else {
		lines = append(lines, truncateSidebarLine(dimStyle.Render("Ctrl+] to focus sidebar"), width))
	}

	// Apply scroll offset.
	if scrollY > 0 {
		if scrollY >= len(lines) {
			scrollY = max(len(lines)-1, 0)
		}
		lines = lines[scrollY:]
	}

	// Pad or trim to exact height.
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}

	return strings.Join(lines, "\n")
}

// truncateSidebarLine truncates a string to at most maxW visible runes,
// appending "..." if truncation occurs. ANSI sequences are counted as-is
// for simplicity since lipgloss handles width internally.
func truncateSidebarLine(s string, maxW int) string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW <= 3 {
		return string(runes[:maxW])
	}
	return string(runes[:maxW-1]) + "\u2026"
}

// wrapSidebarText wraps text into lines of at most maxW runes.
func wrapSidebarText(s string, maxW int) []string {
	if maxW <= 0 || s == "" {
		return []string{s}
	}
	var out []string
	runes := []rune(s)
	for len(runes) > 0 {
		end := maxW
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[:end]))
		runes = runes[end:]
	}
	return out
}
