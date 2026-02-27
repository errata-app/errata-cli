package prompt

import "context"

// ─── Default summarization prompt ────────────────────────────────────────────

// DefaultSummarizationPrompt is used when no recipe-level summarization prompt
// is configured. It is designed to produce high-quality context summaries that
// preserve essential information for conversation continuity.
const DefaultSummarizationPrompt = `Summarize this conversation for context continuity. Preserve:
- All file paths mentioned and their current state
- Decisions made and their rationale
- Errors encountered and how they were resolved
- The current task and its progress
- Code snippets actively being worked on
Discard verbose tool output and abandoned tangents.
Format: Start with "Current task: ..." then list items concisely.
Reply with ONLY the summary.`

// ─── Context-based summarization prompt ──────────────────────────────────────

type sumPromptKey struct{}

// WithSummarizationPrompt returns a context carrying the summarization prompt.
func WithSummarizationPrompt(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, sumPromptKey{}, p)
}

// ResolveSummarizationPrompt returns the summarization prompt from context,
// falling back to DefaultSummarizationPrompt when none is set.
func ResolveSummarizationPrompt(ctx context.Context) string {
	if p, _ := ctx.Value(sumPromptKey{}).(string); p != "" {
		return p
	}
	return DefaultSummarizationPrompt
}
