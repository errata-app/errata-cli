package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/config"
	"github.com/suarezc/errata/internal/tools"
)

// maxHintLines is the maximum number of completion hint lines shown below the
// textarea. Capping prevents layout reflow (viewport bouncing) when many items
// match an empty or short prefix.
const maxHintLines = 8

// hintWriter tracks how many hint lines have been written and enforces the cap.
type hintWriter struct {
	sb    *strings.Builder
	count int
	total int // total matches (including those past the cap)
	style lipgloss.Style // style for the "... and N more" line
}

func newHintWriter(sb *strings.Builder, moreStyle lipgloss.Style) *hintWriter { //nolint:gocritic // lipgloss.Style is idiomatically passed by value
	return &hintWriter{sb: sb, style: moreStyle}
}

// add writes one hint line if under the cap. Returns false when the cap is hit.
func (hw *hintWriter) add(line string) bool {
	hw.total++
	if hw.count >= maxHintLines {
		return false
	}
	hw.sb.WriteByte('\n')
	hw.sb.WriteString(line)
	hw.count++
	return true
}

// flush writes the "... and N more" notice if any items were omitted.
func (hw *hintWriter) flush() {
	if hw.total > hw.count {
		hw.sb.WriteByte('\n')
		hw.sb.WriteString(hw.style.Render(fmt.Sprintf("  ... and %d more", hw.total-hw.count)))
	}
}

// longestCommonPrefix returns the longest string that is a prefix of every
// candidate. Returns "" if candidates is empty.
func longestCommonPrefix(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	prefix := candidates[0]
	for _, c := range candidates[1:] {
		for !strings.HasPrefix(c, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	return prefix
}

// completeArg performs bash-style tab completion of partial against candidates.
// Matching is case-insensitive but returns the candidate's original casing.
// A single match gets a trailing space; multiple matches complete to the
// longest common prefix; no matches return ("", false).
func completeArg(partial string, candidates []string) (string, bool) {
	lower := strings.ToLower(partial)
	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), lower) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return "", false
	case 1:
		return matches[0] + " ", true
	default:
		lcp := longestCommonPrefix(matches)
		if len(lcp) > len(partial) {
			return lcp, true
		}
		return "", false
	}
}

// lastWord returns the last space-separated token, or "" if the string
// is empty or ends with a space (meaning the user hasn't started typing
// the next word yet).
func lastWord(s string) string {
	if s == "" || s[len(s)-1] == ' ' {
		return ""
	}
	words := strings.Split(s, " ")
	return words[len(words)-1]
}

// modelIDCandidates returns the IDs of all configured adapters.
func (a App) modelIDCandidates() []string { //nolint:gocritic // called from bubbletea value-receiver methods
	ids := make([]string, len(a.adapters))
	for i, ad := range a.adapters {
		ids[i] = ad.ID()
	}
	return ids
}

// toolNameCandidates returns the names of all built-in and MCP tools.
func (a App) toolNameCandidates() []string { //nolint:gocritic // called from bubbletea value-receiver methods
	names := make([]string, 0, len(tools.Definitions)+len(a.mcpDefs))
	for _, d := range tools.Definitions {
		names = append(names, d.Name)
	}
	for _, d := range a.mcpDefs {
		names = append(names, d.Name)
	}
	return names
}

// tryArgComplete attempts tab-completion of the last word for commands that
// support argument completion (/model, /tools on, /tools off).
// Returns the full replacement input line and true if completion occurred.
func (a App) tryArgComplete(val string) (string, bool) { //nolint:gocritic // called from bubbletea value-receiver methods
	lower := strings.ToLower(val)

	type argCmd struct {
		prefix     string // lowercase prefix including trailing space
		candidates []string
	}
	cmds := []argCmd{
		{"/model ", a.modelIDCandidates()},
		{"/subset ", a.modelIDCandidates()},
		{"/tools on ", a.toolNameCandidates()},
		{"/tools off ", a.toolNameCandidates()},
		{"/config ", interactiveSections},
		{"/set ", configPathCandidates()},
		{"/keys ", providerNameCandidates()},
	}

	for _, cmd := range cmds {
		if strings.HasPrefix(lower, cmd.prefix) {
			// Use len(cmd.prefix) to slice into the original val,
			// preserving whatever casing the user typed for the command.
			prefix := val[:len(cmd.prefix)]
			argsPart := val[len(cmd.prefix):]
			words := strings.Split(argsPart, " ")
			lastW := words[len(words)-1]

			replacement, ok := completeArg(lastW, cmd.candidates)
			if !ok {
				return "", false
			}
			words[len(words)-1] = replacement
			return prefix + strings.Join(words, " "), true
		}
	}
	return "", false
}

// tryMentionComplete attempts tab-completion when the last word starts with @.
// Returns the completed input line and true if completion occurred.
func (a App) tryMentionComplete(val string) (string, bool) { //nolint:gocritic // called from bubbletea value-receiver methods
	lw := lastWord(val)
	if !strings.HasPrefix(lw, "@") || len(lw) < 2 {
		return "", false
	}
	partial := lw[1:] // strip the @
	replacement, ok := completeArg(partial, a.modelIDCandidates())
	if !ok {
		return "", false
	}
	// Replace the last word with @<completed>
	prefix := val[:len(val)-len(lw)]
	return prefix + "@" + replacement, true
}

// providerNameCandidates returns shorthand names for all supported providers.
func providerNameCandidates() []string {
	providers := config.ProviderEnvInfo()
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.Name
	}
	return names
}

// renderModelHints writes matching model ID suggestions to sb (capped).
func (a App) renderModelHints(sb *strings.Builder, partial string, nameStyle lipgloss.Style) { //nolint:gocritic // called from bubbletea value-receiver methods
	lp := strings.ToLower(partial)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	hw := newHintWriter(sb, dimStyle)
	for _, id := range a.modelIDCandidates() {
		if strings.HasPrefix(strings.ToLower(id), lp) {
			hw.add(nameStyle.Render("  " + id))
		}
	}
	hw.flush()
}

// renderToolHints writes matching tool name suggestions to sb (capped).
func (a App) renderToolHints(sb *strings.Builder, partial string, nameStyle, descStyle lipgloss.Style) { //nolint:gocritic // called from bubbletea value-receiver methods
	lp := strings.ToLower(partial)
	hw := newHintWriter(sb, descStyle)
	for _, d := range tools.Definitions {
		if strings.HasPrefix(strings.ToLower(d.Name), lp) {
			hw.add(nameStyle.Render("  " + d.Name))
		}
	}
	for _, d := range a.mcpDefs {
		if strings.HasPrefix(strings.ToLower(d.Name), lp) {
			hw.add(nameStyle.Render("  "+d.Name) + descStyle.Render("  (mcp)"))
		}
	}
	hw.flush()
}
