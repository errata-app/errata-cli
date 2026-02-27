// Package commands defines the canonical list of Errata slash commands.
// The TUI (internal/ui) derives its command list from this package.
package commands

// Command describes a single slash command.
type Command struct {
	Name string // e.g. "/clear"
	Desc string // one-line description shown in suggestions and /help
}

// All is the ordered canonical list of slash commands.
var All = []Command{
	{"/help", "Show available commands"},
	{"/keys", "View provider status and add API keys: /keys <provider> <key>"},
	{"/clear", "Clear display (preserves conversation context)"},
	{"/wipe", "Wipe display and conversation memory"},
	{"/compact", "Summarise conversation history to free up context"},
	{"/verbose", "Toggle verbose mode"},
	{"/models", "List active and all available models by provider"},
	{"/model", "Restrict to model(s); bare /model resets to all"},
	{"/tools", "Enable/disable tools: /tools off bash; /tools on bash; /tools reset"},
	{"/seed", "Set seed for reproducibility; bare /seed clears"},
	{"/subset", "Target specific model(s); bare /subset shows current"},
	{"/all", "Reset to all models"},
	{"/config", "View/edit configuration; /config <section> jumps to section"},
	{"/set", "Set config: /set <path> <value>; bare path shows current"},
	{"/resume", "Resume interrupted run — re-runs only interrupted models"},
	{"/remind", "Fire a named reminder; bare /remind lists available"},
	{"/export", "Export: /export recipe [path]; /export output [path]"},
	{"/import", "Import: /import recipe <path>"},
	{"/stats", "Show preference wins and session cost"},
	{"/totalcost", "Show total inference cost for this session"},
	{"/exit", "Exit"},
}
