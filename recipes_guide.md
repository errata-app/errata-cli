# Recipes Guide

## What is a Recipe?

A recipe is a Markdown file that defines a model comparison. One prompt, one tool set, one set of constraints, multiple models. Every model gets identical conditions — same system prompt, same tools, same step limits — so the only variable is the model itself.

## Recipe Format

A recipe is a Markdown file with a `version:` header and `##` sections. Here is each section with an example.

### Version (required)

Declared before the first `##` section. Currently only version 1 is supported.

```markdown
# My Recipe
version: 1
```

### Models

Which models to run. Use `provider/model` format for OpenRouter models, or bare IDs for native providers.

```markdown
## Models
- anthropic/claude-sonnet-4-6
- openai/gpt-4o
- google/gemini-2.5-flash
- meta-llama/llama-3.1-8b-instruct
```

### System Prompt

Instructions given to every model as a system message. Free-form text.

```markdown
## System Prompt
You are an expert Go developer debugging failing test suites.

Rules:
- Do NOT modify test files — only fix the source code
- Be precise and minimal in your fixes
- After proposing your fix, explain what the bug was in one sentence
```

### Tools

Which built-in tools models can use. Omit this section entirely to enable all tools.

Available built-in tools:
- `read_file` — read file contents with optional offset/limit
- `write_file` — propose a file write (intercepted, not written to disk during the run)
- `edit_file` — exact-string-replace in a file
- `list_directory` — directory tree listing
- `search_files` — glob-based file search
- `search_code` — regex content search
- `bash` — shell command execution (2-minute default timeout)
- `web_fetch` — fetch and clean a public URL
- `web_search` — DuckDuckGo instant answers

You can restrict bash to specific command prefixes:

```markdown
## Tools
- read_file
- edit_file
- bash(go test, go build, go vet)
- search_code
```

### Constraints

Limits on the agentic loop.

- `max_steps` — maximum tool-call iterations per model (0 = unlimited)
- `timeout` — wall-clock time limit per model (duration string or bare seconds)
- `bash_timeout` — per-bash-call timeout (default: 2m)

```markdown
## Constraints
max_steps: 50
timeout: 5m
bash_timeout: 30s
```

### Tasks

What the models should accomplish. Each bullet is a separate task prompt sent to all models. Multiple tasks run sequentially by default.

```markdown
## Tasks
- The Go project at challenge01/ has failing tests. Run `cd challenge01 && go test -v ./...` to see the failures. Find and fix the bug. Do not modify test files.
- The Go project at challenge02/ has failing tests. Run `cd challenge02 && go test -v ./...` to see the failures. Find and fix the bug. Do not modify test files.
```

### Success Criteria

How to evaluate each model's response in headless mode. Omit for interactive use. Each model's proposed writes are applied to an isolated worktree before criteria are checked, so `run:` commands execute against the model's actual output.

Available criteria:
- `no_errors` — the model completed without errors
- `has_writes` — the model proposed at least one file write
- `files_written >= N` — the model proposed at least N file writes
- `contains: <text>` — the model's response text contains the given substring
- `run: <command>` — execute a shell command in the model's worktree; passes if exit code is 0
- `run(timeout=N): <command>` — same as `run:` with a custom timeout in seconds (default: 60)
- `tool_used: <name>` — the model called the named tool at least once
- `max_cost: <float>` — the model's total cost stayed under the given USD threshold
- `max_latency: <int>` — the model finished within the given milliseconds
- `max_tool_calls: <int>` — the model used no more than this many total tool calls

```markdown
## Success Criteria
- no_errors
- files_written >= 2
- tool_used: edit_file
- run: cd go_gauntlet_test/challenge11_docstore && go test -v ./...
- max_cost: 0.50
```

## Example Recipes

### `go_docstore.md` — Multi-File DocStore Bug Fix

A multi-file in-memory document store with bugs spread across `collection.go`, `index.go`, `query.go`, and `document.go`. Models must trace failures across file boundaries and fix multiple interacting bugs. Tests which model can hold a multi-file mental model. An interesting result: models that fix each file in isolation often miss the index/query interaction bug.

```
errata run go_docstore.md --verbose
```

## Writing Good Recipes

Hard tasks differentiate models — if every model passes, the recipe isn't useful. Easy tasks find where you can use cheaper models — run the same task with a $0.001/call model and a $0.10/call model. Always include success criteria so headless runs can self-evaluate without human review. Keep system prompts short and directive — models follow explicit rules better than vague guidance. One task per concept makes results easier to interpret than multi-part prompts.
