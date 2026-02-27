# My Project Recipe

## Metadata
name: My Project Recipe
description: Full-featured example recipe demonstrating every configuration option
tags: go, example, reference
author: yourname
version: 1.0
extends: ~/.errata/recipes/base.md
contribute: false
project_root: /path/to/project

## Models
- claude-sonnet-4-6
- gpt-4o
- gemini-2.0-flash

## System Prompt
You are working on a Go monorepo. Always run `go vet ./...` and `go test ./...` after
making any changes. Use conventional commits (feat:, fix:, chore:). Keep functions
small and focused; prefer table-driven tests.

## System Prompt Variants
### concise
Go monorepo. Run `go vet` and `go test` after changes. Use conventional commits.

### minimal
Go project. Test after changes.

## System Prompt Overrides
### gpt-4o
You are working on a Go monorepo. Always run `go vet ./...` and `go test ./...` after
making any changes. Use conventional commits (feat:, fix:, chore:). Keep functions
small and focused. Prefer returning errors over panicking.

### gemini-2.0-flash
concise

## Tools
- read_file
- list_directory
- search_files
- search_code
- edit_file
- write_file
- web_fetch
- web_search
- bash(go test *, go build *, go vet *, gofmt *, make *)

## Tool Descriptions
### bash
Use bash for running tests, builds, and linting only. Always check exit codes.
Do not use bash for file manipulation — use write_file or edit_file instead.

### read_file
Read file contents. Prefer reading specific line ranges with offset/limit for
large files rather than reading the entire file.

### search_code
Regex search across the codebase. Use this before editing to understand existing
patterns. Always search for usages before renaming or deleting a function.

## Tool Description Variants
### bash
#### concise
Run tests, builds, and lints. Check exit codes.

### search_code
#### concise
Regex search. Search before editing.

## Tool Description Overrides
### gemini-2.0-flash
#### bash
Run shell commands for builds and tests only. Check exit codes.
#### search_code
Regex search across files. Search for usages before making changes.

## MCP Servers
- exa: npx @exa-ai/exa-mcp-server
- brave: npx @modelcontextprotocol/server-brave-search

## Model Parameters
seed: 42

## Constraints
timeout: 10m
bash_timeout: 2m
max_steps: 50

## Context
max_history_turns: 30
strategy: auto_compact
compact_threshold: 0.75
task_mode: independent

## Context Summarization Prompt
Summarize this conversation for context continuity. Preserve:
- All file paths mentioned and their current state
- Decisions made and their rationale
- Errors encountered and how they were resolved
- The current task and its progress
- Any Go package dependencies or import relationships discussed
Discard verbose tool output, full file contents, and abandoned approaches.
Format: Start with "Current task: ..." then list key items concisely.
Reply with ONLY the summary.

## Context Summarization Prompt Variants
### concise
Summarize the conversation. Keep file paths, decisions, errors, and current task.
Discard verbose output. Start with "Task: ...". Reply with ONLY the summary.

## System Reminders
### context_warning
trigger: context_usage > 0.75

You are approaching the context limit. Be concise in your responses. Avoid
re-reading files you have already seen. If you need to make multiple changes,
batch them into fewer tool calls.

### many_turns
trigger: turn_count >= 20

You have been working for many turns. Consider whether you are making progress
or going in circles. If stuck, step back and re-evaluate your approach.

### tool_failure
trigger: last_tool_call_failed

The last tool call failed. Read the error message carefully before retrying.
Common causes: wrong file path, invalid regex, or syntax error in edit.

### focus_reminder
trigger: manual

Remember: focus on the specific task requested. Do not refactor surrounding code
or add features beyond what was asked. Keep changes minimal and targeted.

## Hooks
### post_edit_vet
event: post_tool_use
matcher: edit_file
command: go vet ./... 2>&1 | head -20
timeout: 30s
inject_output: true

### post_edit_test
event: post_tool_use
matcher: edit_file
command: go test ./... -count=1 -short 2>&1 | tail -20
timeout: 60s
inject_output: true

### session_start_check
event: session_start
command: echo "Go version:" && go version && echo "Module:" && head -1 go.mod
timeout: 10s
inject_output: true

## Output Processing
### bash
max_lines: 200
truncation: tail
truncation_message: [Truncated to last {max_lines} lines. Full output: {line_count} lines]

### search_code
max_lines: 100
truncation: head_tail

### read_file
max_lines: 500
truncation: head

## Output Processing Overrides
### gemini-2.0-flash
#### bash
max_lines: 50
truncation: tail
#### search_code
max_lines: 30
truncation: head_tail

## Model Profiles
### gpt-4o
context_budget: 128000
tool_format: function_calling

### gemini-2.0-flash
context_budget: 1000000
tool_format: function_calling
tier: concise

### local-llama
context_budget: 8192
tool_format: text_in_prompt
tier: minimal
mid_convo_system: false

## Sandbox
filesystem: project_only
network: full
allow_local_fetch: true

## Tasks
- Audit all Go files for unused error return values and fix them
- Add table-driven tests for any function with fewer than two test cases
- Run `go vet ./...` and resolve every reported issue

## Success Criteria
- no_errors
- has_writes
