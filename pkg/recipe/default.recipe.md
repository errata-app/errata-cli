# Errata Default
version: 1

## Context
max_history_turns: 20
strategy: auto_compact
compact_threshold: 0.80

## Constraints
timeout: 5m
bash_timeout: 2m
max_steps: 0

## Sandbox
filesystem: unrestricted
network: full
allow_local_fetch: false

## Tools
- list_directory: Use list_directory to explore the project structure before reading specific files.
- search_files: Use search_files to find files by name pattern (e.g. search_files("**/*.go")).
- search_code: Use search_code to find where a function, type, or string is defined or used.
- read_file: Use read_file only after you know which file you need. For large files, use offset and limit to page through content.
- edit_file: Use edit_file for targeted, surgical changes to existing files by replacing an exact string match.
- write_file: Use write_file for new files or complete rewrites. Use edit_file for targeted changes.
- bash: Use bash to run tests, builds, or any shell command; always provide a clear description.
- web_fetch: Use web_fetch to read documentation, GitHub issues, package READMEs, or any public URL.
- web_search: Use web_search for quick factual lookups (definitions, Wikipedia summaries). For specific URLs, use web_fetch directly.

## Context Summarization Prompt
Summarize this conversation for context continuity. Preserve:
- All file paths mentioned and their current state
- Decisions made and their rationale
- Errors encountered and how they were resolved
- The current task and its progress
- Code snippets actively being worked on
Discard verbose tool output and abandoned tangents.
Format: Start with "Current task: ..." then list items concisely.
Reply with ONLY the summary.
