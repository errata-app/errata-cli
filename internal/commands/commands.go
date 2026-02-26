// Package commands defines the canonical list of Errata slash commands.
// Both the TUI (internal/ui) and the web server (internal/web) derive their
// command lists from this package, ensuring descriptions stay in sync across
// surfaces without duplication.
package commands

// Command describes a single slash command.
type Command struct {
	Name    string // e.g. "/clear"
	Desc    string // one-line description shown in suggestions and /help
	TUIOnly bool   // true for commands that have no meaning in a browser session
}

// All is the ordered canonical list of slash commands.
// TUIOnly commands are omitted from the /api/commands REST response.
var All = []Command{
	{"/help",      "Show available commands",                           false},
	{"/clear",     "Clear display history and conversation memory",     false},
	{"/compact",   "Summarise conversation history to free up context", false},
	{"/verbose",   "Toggle verbose mode",                               false},
	{"/models",    "List active and all available models by provider",  false},
	{"/model",     "Restrict to model(s); bare /model resets to all",   false},
	{"/tools",     "Enable/disable tools: /tools off bash; /tools on bash; /tools reset", false},
	{"/seed",      "Set seed for reproducibility; bare /seed clears",   false},
	{"/subset",    "Target specific model(s); bare /subset shows current", false},
	{"/all",       "Reset to all models",                                false},
	{"/config",    "View/edit configuration; /config <section> jumps to section", false},
	{"/set",       "Set config: /set <path> <value>; bare path shows current",    false},
	{"/resume",    "Resume interrupted run — re-runs only interrupted models", false},
	{"/remind",    "Fire a named reminder; bare /remind lists available",        false},
	{"/export",    "Export: /export recipe [path]; /export output [path]", true},
	{"/import",    "Import: /import recipe <path>",                      true},
	{"/stats",     "Show preference wins and session cost",             false},
	{"/totalcost", "Show total inference cost for this session",        false},
	{"/exit",      "Exit",                                              true},
}

// Web returns the subset of All that is applicable to the web UI
// (i.e. all commands where TUIOnly is false).
func Web() []Command {
	var out []Command
	for _, c := range All {
		if !c.TUIOnly {
			out = append(out, c)
		}
	}
	return out
}
