# Errata Quickstart

Get up and running in under five minutes — first with the interactive TUI, then with
the headless runner for automated evaluation.

---

## 1. Install and configure

```bash
git clone https://github.com/suarezc/Errata
cd Errata
go build -o errata ./cmd/errata
```

Create a `.env` file with at least one API key:

```bash
cp .env.example .env
```

```bash
# .env — add whichever keys you have
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...
```

Errata auto-detects providers from available keys and activates one default model per
provider (e.g. `claude-sonnet-4-6`, `gpt-4o`, `gemini-2.0-flash`). No further
configuration is needed to start. See [defaults.md](defaults.md) for the full list of
defaults.

---

## 2. Interactive TUI

Launch the REPL:

```bash
./errata
```

Type a prompt and press Enter. All configured models receive it concurrently:

```
> Read internal/config/config.go and add a Validate() method
```

Live panels show each model's tool activity as it works:

```
+-- claude-sonnet-4-6  running... --+  +-- gpt-4o  running... -----------+
| reading  .                        |  | reading  internal/config/        |
| reading  internal/config/config.go|  | reading  internal/config/config.go
| writing  internal/config/config.go|  | bash     go vet ./...            |
+-----------------------------------+  | writing  internal/config/config.go
                                       +---------------------------------+
```

When models finish, a diff view shows each proposal with latency, token count, and cost.
Pick a number to apply that model's writes to disk, or `s` to skip.

### Key commands

| Command | What it does |
|---------|-------------|
| `/config` | Browse and edit session configuration interactively |
| `/verbose` | Toggle model reasoning text alongside tool events |
| `/compact` | Summarize conversation history to free context window |
| `/stats` | Show preference win counts and session cost per model |
| `/resume` | Re-run only interrupted models after cancelling with ESC |
| `/help` | List all commands |

See [README.md](README.md#repl-commands) for the complete command reference and keyboard
shortcuts.

---

## 3. Recipes

A **recipe** is a Markdown file that configures models, prompts, tools, and tasks. Errata
auto-discovers `recipe.md` in the current directory, or you can specify one explicitly:

```bash
./errata -r path/to/recipe.md
```

### Minimal TUI recipe

```markdown
# My Project

## Models
- claude-sonnet-4-6
- gpt-4o

## System Prompt
You are working on a Python project. Always run pytest before proposing changes.
```

### Adding tool restrictions

Restrict which tools models can use, and limit bash to specific command prefixes:

```markdown
## Tools
- read_file
- list_directory
- search_files
- search_code
- edit_file
- write_file
- bash(go test *, go build *, make *)
```

### Configuring MCP servers

Extend the tool set with any [MCP](https://modelcontextprotocol.io/)-compatible server:

```markdown
## MCP Servers
- exa: npx @exa-ai/exa-mcp-server
```

The server's tools are discovered at startup and available to every model. Set the
server's API key (e.g. `EXA_API_KEY`) in `.env`.

See [recipe.example.md](recipe.example.md) for every available section with examples.

---

## 4. Headless runner (`errata run`)

The headless runner executes recipe tasks without user interaction — useful for CI,
batch evaluation, and automated benchmarking.

### Requirements

A recipe with a `## Tasks` section. Each bullet is a separate task prompt:

```markdown
# Eval: Go Lint Fixes

## Models
- claude-sonnet-4-6
- gpt-4o
- gemini-2.0-flash

## System Prompt
You are working on a Go project at the current directory.
Run `go vet ./...` and `go test ./...` to validate your changes.

## Tasks
- Fix all lint warnings from `golangci-lint run ./...`
- Add table-driven tests for any function with fewer than two test cases
- Audit error handling: find unchecked error returns and fix them

## Success Criteria
- no_errors
- has_writes
```

### Running

```bash
# Run with auto-discovered recipe.md
./errata run

# Run with a specific recipe
./errata run -r eval-recipe.md

# Print a JSON report to stdout (for piping to jq or another tool)
./errata run --json

# Save report to a custom directory
./errata run --output-dir results/

# Verbose output (show model reasoning text during runs)
./errata run --verbose
```

### What happens

1. Each task is sent to every model concurrently
2. Models work autonomously using the configured tool set
3. Success criteria are evaluated against each response
4. A summary prints to stderr with per-model results (latency, cost, criteria)
5. A JSON report is saved to `data/outputs/` (or `--output-dir`)

Example output:

```
Task 1/3: Fix all lint warnings from `golangci-lint run ./...`
  claude-sonnet-4-6   done  4231ms  $0.0142  [no_errors: pass] [has_writes: pass]
  gpt-4o              done  3892ms  $0.0067  [no_errors: pass] [has_writes: pass]
  gemini-2.0-flash    done  2104ms  $0.0023  [no_errors: pass] [has_writes: pass]

Task 2/3: Add table-driven tests...
  ...

Summary:
  claude-sonnet-4-6   3/3 passed  $0.0391  avg 3841ms
  gpt-4o              2/3 passed  $0.0185  avg 3102ms
  gemini-2.0-flash    3/3 passed  $0.0071  avg 1897ms
```

### Task modes

Control how tasks interact with each other via the `## Context` section:

```markdown
## Context
task_mode: sequential
```

- **`independent`** (default) — each task runs in isolation; histories are reset between tasks.
- **`sequential`** — tasks run in order; the best model's writes are applied to disk before the next task starts, so later tasks build on earlier results.

### Interruption and resume

Cancel a headless run with `Ctrl-C`. A checkpoint is saved automatically. Re-run the
same command to resume from where it left off.

---

## 5. Viewing preference data

Every time you pick a winner in the TUI, the choice is logged. View your accumulated
preferences:

```bash
# Quick summary
./errata stats

# Detailed: win rate, average latency, average cost per model
./errata stats --detail
```

---

## 6. Debug logging

Enable full JSONL logging of every prompt, tool event, and response:

```bash
./errata --debug-log data/log.jsonl
./errata run --debug-log data/log.jsonl
```

Each log entry includes model ID, token counts, latency, cost, and all tool calls.

---

## Further reading

- **[README.md](README.md)** — full feature reference (all commands, model filtering, MCP servers, context management, interruption/resume)
- **[recipe.example.md](recipe.example.md)** — complete recipe with every configuration section
- **[defaults.md](defaults.md)** — what a fresh binary sends to models with no recipe
