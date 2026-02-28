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
	{"/clear", "Clear display (preserves conversation context)"},
	{"/wipe", "Wipe display and conversation memory"},
	{"/compact", "Summarise conversation history to free up context"},
	{"/verbose", "Toggle verbose mode"},
	{"/config", "View/edit configuration; /config <section> jumps to section"},
	{"/config-pin", "Toggle pinned config sidebar"},
	{"/resume", "Resume interrupted run — re-runs only interrupted models"},
	{"/rewind", "Undo last run — revert writes and remove from context"},
	{"/export", "Export: /export recipe [path]; /export output [path]"},
	{"/import", "Import: /import recipe <path>"},
	{"/stats", "Show preference wins and session cost"},
	{"/exit", "Exit"},
}
