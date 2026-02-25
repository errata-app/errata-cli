package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/suarezc/errata/internal/tools"
)

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
func (a App) modelIDCandidates() []string {
	ids := make([]string, len(a.adapters))
	for i, ad := range a.adapters {
		ids[i] = ad.ID()
	}
	return ids
}

// toolNameCandidates returns the names of all built-in and MCP tools.
func (a App) toolNameCandidates() []string {
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
func (a App) tryArgComplete(val string) (string, bool) {
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
func (a App) tryMentionComplete(val string) (string, bool) {
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

// renderModelHints writes matching model ID suggestions to sb.
func (a App) renderModelHints(sb *strings.Builder, partial string, nameStyle lipgloss.Style) {
	lp := strings.ToLower(partial)
	for _, id := range a.modelIDCandidates() {
		if strings.HasPrefix(strings.ToLower(id), lp) {
			sb.WriteByte('\n')
			sb.WriteString(nameStyle.Render("  " + id))
		}
	}
}

// renderToolHints writes matching tool name suggestions to sb.
func (a App) renderToolHints(sb *strings.Builder, partial string, nameStyle, descStyle lipgloss.Style) {
	lp := strings.ToLower(partial)
	for _, d := range tools.Definitions {
		if strings.HasPrefix(strings.ToLower(d.Name), lp) {
			sb.WriteByte('\n')
			sb.WriteString(nameStyle.Render("  " + d.Name))
		}
	}
	for _, d := range a.mcpDefs {
		if strings.HasPrefix(strings.ToLower(d.Name), lp) {
			sb.WriteByte('\n')
			sb.WriteString(nameStyle.Render("  " + d.Name))
			sb.WriteString(descStyle.Render("  (mcp)"))
		}
	}
}
