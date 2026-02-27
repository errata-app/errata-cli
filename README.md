# Errata

A/B testing tool for agentic AI models. Send a prompt to multiple models simultaneously,
watch each one navigate your codebase live, pick the best proposal, and apply it to disk.
Every choice is logged so you can see which models you actually prefer over time.

---

## What it does

1. You type a prompt in the Errata REPL
2. All configured models receive it concurrently, each running as a full coding agent
3. Models navigate your codebase using nine built-in tools: list directories, search files
   by name or content, read files, run shell commands, fetch URLs, search the web, and
   propose file changes — plus any tools exposed by MCP servers you configure
4. Live panels show each model's tool activity in real time
5. Once all models finish, a diff view shows exactly what each one wants to change
6. You pick a winner by number — that model's writes are applied to disk
7. Your choice is appended to a local preference log

---

## Requirements

- Go 1.23+
- At least one API key: Anthropic, OpenAI, Google, or OpenRouter

---

## Installation

```bash
git clone https://github.com/suarezc/Errata
cd Errata
go build -o errata ./cmd/errata
```

Or install directly to `$GOPATH/bin`:

```bash
go install github.com/suarezc/errata/cmd/errata@latest
```

Copy the environment template and fill in your keys:

```bash
cp .env.example .env
```

```bash
# .env — only API keys and credentials; all behavioural config is in recipe.md

# Native providers — auto-detected; one default model per available key
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...

# OpenRouter — single key for any model from any provider
OPENROUTER_API_KEY=sk-or-...

# LiteLLM — self-hosted proxy (base URL must include /v1)
LITELLM_BASE_URL=http://localhost:4000/v1
LITELLM_API_KEY=optional

# Amazon Bedrock — uses AWS SDK credential chain
AWS_REGION=us-east-1

# Azure OpenAI — both key and endpoint required
AZURE_OPENAI_API_KEY=...
AZURE_OPENAI_ENDPOINT=https://myresource.openai.azure.com

# Vertex AI — uses Application Default Credentials
VERTEX_AI_PROJECT=my-gcp-project
VERTEX_AI_LOCATION=us-central1
```

Errata auto-detects providers from available keys/credentials:

| Provider   | Default model         | Env vars required |
|------------|-----------------------|-------------------|
| Anthropic  | `claude-sonnet-4-6`   | `ANTHROPIC_API_KEY` |
| OpenAI     | `gpt-4o`              | `OPENAI_API_KEY` |
| Google AI  | `gemini-2.0-flash`    | `GOOGLE_API_KEY` |
| Bedrock    | `anthropic.claude-sonnet-4-*` | `AWS_REGION` + AWS credentials |
| Azure OpenAI | `gpt-4o`           | `AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_ENDPOINT` |
| Vertex AI  | `gemini-2.0-flash`    | `VERTEX_AI_PROJECT` + `VERTEX_AI_LOCATION` |

OpenRouter, LiteLLM, and cloud-provider models must be listed explicitly in a recipe
`## Models` section (e.g. `anthropic/claude-sonnet-4-6`, `litellm/codestral`,
`bedrock/anthropic.claude-sonnet-4-*`).

---

## Usage

### TUI (terminal REPL)

```bash
./errata                     # auto-discovers recipe.md in cwd
./errata -r path/to/recipe.md
```

### Headless mode (recipe runner)

```bash
./errata run                     # run recipe tasks (requires recipe.md with ## Tasks)
./errata run --json              # print JSON report to stdout
./errata run --output-dir out/   # save report to custom directory
./errata run -r path/to/my.md    # use a specific recipe file
```

Runs all tasks defined in a recipe file against all configured models without user
interaction. Each task is sent to every model concurrently; results are compared using
optional success criteria and saved as a JSON report. See [Recipes](#recipes) below.

### Preference summary

```bash
./errata stats
./errata stats --detail   # includes win rate, avg latency, avg cost
```

Prints a ranked tally of how often each model has been selected across all past runs.

---

## Running a prompt

Prompts work best when they reference actual files in your working directory:

```
Read src/utils/retry.py and add exponential backoff to the retry decorator
```

Live panels show each model's tool activity as it works:

```
╭── claude-sonnet-4-6  running… ────╮  ╭── gpt-4o  running… ────────────╮
│ reading  .                         │  │ reading  .                      │
│ reading  **/*.go                   │  │ reading  src/utils/retry.py     │
│ bash     go test ./...             │  │ writing  src/utils/retry.py     │
│ writing  src/utils/retry.py        │  │                                 │
╰────────────────────────────────────╯  ╰─────────────────────────────────╯
```

Once all models finish, a diff view shows exactly what each proposed, along with latency,
token usage, and estimated cost:

```
── claude-sonnet-4-6  891ms  ·  8.4k tok  ·  $0.0083 ─────────────────
    src/utils/retry.py  +12 -3
    + def retry(max_attempts=3, backoff=2.0):
    -     time.sleep(1)
    +     time.sleep(backoff ** attempt)
    … 4 more lines

── gpt-4o  1243ms  ·  6.1k tok  ·  $0.0031 ───────────────────────────
    src/utils/retry.py  +8 -1
    +     delay = min(base * 2 ** attempt, max_delay)
```

Then the selection prompt:

```
Select a response to apply:
  1  claude-sonnet-4-6             (891ms $0.0083)   →  src/utils/retry.py
  2  gpt-4o                        (1243ms $0.0031)  →  src/utils/retry.py
  s  Skip

choice>
```

Pick a number — that model's writes are applied to disk immediately.

---

## REPL commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/clear` | Clear display (preserves conversation context) |
| `/wipe` | Wipe display and conversation memory |
| `/compact` | Summarise conversation history to free up context |
| `/verbose` | Toggle verbose mode |
| `/config` | View/edit configuration; `/config <section>` jumps to section |
| `/resume` | Resume interrupted run — re-runs only interrupted models |
| `/export recipe [path]` | Export the session recipe to Markdown (default: `recipe_export.md`) |
| `/export output [path]` | Export the latest run's output report |
| `/import recipe <path>` | Import a recipe file, replacing the session config |
| `/stats` | Show preference wins and session cost |
| `/exit` or `/quit` | Exit |
| `Ctrl-D` | Exit |

**TUI input shortcuts:**

| Key | Action |
|-----|--------|
| `ESC` or `Ctrl-C` | Cancel the current run (partial results are preserved; use `/resume` to continue) |
| `↑` (line 0) | Recall previous prompt (cycle backward through history) |
| `↓` (while navigating) | Cycle forward; at newest restores original typed input |
| `Ctrl-R` | Open reverse-i-search: type a substring to filter history; `Ctrl-R` again for next match; `Enter` to select; `Escape` to cancel |
| `Tab` | Complete the current `/command` prefix |
| `PgUp` / `PgDn` | Scroll the conversation feed |

---

## Model filtering

You can control which models run via the recipe `## Models` section or at runtime via
`/config` (models section).

**Via recipe:**

```markdown
## Models
- claude-sonnet-4-6
- gpt-4o
- anthropic/claude-opus-4-6      # OpenRouter format
- litellm/codestral              # LiteLLM format
```

**At runtime:** Use `/config models` to add, remove, or reorder models for the current
session. Changes are session-local — use `/export recipe` to persist them.

---

## Interactive configuration (`/config`)

The `/config` command opens a full-screen overlay for browsing and editing the session
recipe. Navigate sections with arrow keys, expand with Enter, and collapse with Escape.

Each section shows a brief description and its current summary. Scalar fields (like
`constraints.timeout` or `context.max_history_turns`) display their default values when
no override is set. Text sections (system prompt, context summarization prompt) support
inline editing — press Enter to open the editor, Ctrl+S to save, Escape to cancel.

Jump directly to a section: `/config constraints`, `/config system-prompt`, etc.

Changes made in `/config` are session-local. Use `/config reset` to revert to recipe
defaults, or `/export recipe` to save your modifications.

---

## Recipe export and import

Export the current session recipe (including any `/config` modifications) to
a Markdown file:

```
/export recipe                    # writes recipe_export.md in cwd
/export recipe ~/my-recipe.md     # custom path
```

Import a recipe file, replacing the current session configuration:

```
/import recipe path/to/recipe.md
```

Export the latest run's output report:

```
/export output                    # writes to data/outputs/
/export output ~/reports/         # custom directory
```

---

## Verbose mode

Toggle `/verbose` to also see each model's explanatory text alongside tool events. Verbose
mode is off by default.

---

## Context window management

Errata maintains a per-model conversation history across prompts. History is saved to
`data/history.json` after every run so you can close the client and pick up exactly
where you left off. Use `/wipe` to deliberately wipe it. Each panel's status line shows
the estimated context fill (e.g. `~42% ctx`) so you can see how much of a model's window
is in use.

Two mechanisms keep history from growing unbounded:

**Sliding window (automatic):** Only the most recent 20 turns are sent to the model on
each call. Override via recipe `## Context` section (`max_history_turns:`).

**Compaction (manual or automatic):** `/compact` asks each model to write a concise
summary of the conversation so far, then replaces the full history with that summary.
This preserves continuity while freeing context. Compaction also triggers automatically
when a model's estimated history fill reaches 80%.

---

## Interruption and resume

You can cancel a running prompt at any time. Partial results (text generated so far,
proposed file writes, token counts) are preserved — nothing is thrown away.

**How to cancel:**

| Surface | Method |
|---------|--------|
| TUI | Press `ESC` or `Ctrl-C` while models are running |
| Headless (`errata run`) | Send `SIGINT` (Ctrl-C) or `SIGTERM` |

When a run is cancelled, models that had already finished keep their full results. Models
that were still in progress are marked as "interrupted" with whatever partial output they
had accumulated.

A checkpoint is automatically saved to `data/checkpoint.json`. To pick up where you left
off, use `/resume` — this re-runs only the interrupted models from scratch while keeping
the completed models' results intact.

```
> /resume
[resume] Read src/utils/retry.py and add exponential backoff...
```

If you interrupt again during a resume, the checkpoint is updated and you can `/resume`
once more. The checkpoint is cleared automatically after any successful (non-interrupted)
run completes.

**Crash resilience:** Checkpoints are written incrementally at each adapter turn boundary,
not just after the run finishes. This means partial work survives even ungraceful
termination (kill -9, OOM kill, power loss) — use `/resume` to pick up from the last
completed turn.

---

## MCP tool servers

Errata supports the [Model Context Protocol](https://modelcontextprotocol.io/) (MCP),
which lets you connect any MCP-compatible tool server (web search, code execution,
databases, etc.) and expose its tools to every model in the comparison harness.

### Configuration

MCP servers are configured in the recipe `## MCP Servers` section:

```markdown
## MCP Servers
- exa: npx @exa-ai/exa-mcp-server
- search: npx @modelcontextprotocol/server-brave-search
```

Each entry is a `name: command` pair. The command is launched as a subprocess using stdio
transport (the standard MCP deployment model). Errata performs the MCP handshake at startup,
discovers all tools the server exposes, and registers them alongside the nine built-in tools.
API keys needed by the MCP server (e.g. `EXA_API_KEY`) should be set in `.env`.

### Supported servers (examples)

| Provider | Package | Tools exposed |
|----------|---------|---------------|
| Exa AI | `npx @exa-ai/exa-mcp-server` | `search`, `find_similar`, `get_contents` |
| Brave Search | `npx @modelcontextprotocol/server-brave-search` | `brave_web_search` |
| Tavily | `npx @tavily-ai/tavily-mcp` | `tavily_search` |
| Filesystem | `npx @modelcontextprotocol/server-filesystem` | `read_file`, `write_file`, etc. |

Any MCP server that uses stdio transport and exposes the `tools` capability will work.

### Error handling

If an MCP server fails to start or the handshake fails, Errata continues without it and
emits a warning:

- **TUI:** warning printed to stderr before the REPL starts

Runtime errors during a tool call (e.g. the MCP server crashes mid-run) are surfaced in
the live panel as an error event so you can see the failure in context.

### Managing MCP tools at runtime

MCP tools appear alongside built-in tools in `/config` (tools section) and can be toggled
the same way. Disabling a tool via `/config` works identically for both built-in and MCP
tool names.

---

## Deployment configuration

All behavioural configuration is done via recipe files. See [Recipes](#recipes) for the
full list of sections.

### Custom system prompt

Use the recipe `## System Prompt` section to append instructions to every model's system
prompt:

```markdown
## System Prompt
This project uses Python 3.11 and pytest. Always run pytest before proposing changes.
Follow PEP 8 strictly.
```

The extra text is appended after the built-in tool guidance in each model's system prompt.

### Restricting the tool set

Use the recipe `## Tools` section to specify an allowlist of tools. Tools not in the
allowlist are excluded. Tool state can also be toggled at runtime via `/config` (tools
section).

### Restricting to specific models

```markdown
## Models
- claude-opus-4-6
- gpt-4o
```

### Pointing at a self-hosted model proxy

Set `LITELLM_BASE_URL` in `.env` and specify models via recipe:

```markdown
## Models
- litellm/llama-3-70b
- litellm/codestral
```

### Debug logging

```bash
./errata --debug-log data/log.jsonl
```

Each log entry includes the model ID, session ID, all tool events, token counts, latency,
and cost. Useful for auditing or building fine-tuning datasets.

### Context window

Override the default sliding window size via recipe `## Context` section:

```markdown
## Context
max_history_turns: 10
```

---

## Recipes

A recipe is a Markdown file (`recipe.md`) that configures Errata for a specific project or
workflow. Errata auto-discovers `recipe.md` in the current directory, or you can specify one
with `--recipe path/to/file.md` (or `-r`).

Recipes are used by both surfaces (TUI and headless) and can configure models, system
prompts, tools, context management, and more. The headless `errata run` command additionally
requires a `## Tasks` section.

### Minimal example

```markdown
# My Project

## Models
- claude-sonnet-4-6
- gpt-4o

## System Prompt
You are working on a Go project. Run `go test ./...` after changes.

## Tasks
- Add table-driven tests for utils.go
- Fix all lint warnings from `golangci-lint run`

## Success Criteria
- no_errors
- has_writes
```

### Available sections

| Section | Purpose |
|---------|---------|
| `## Models` | List of model IDs to use (overrides env config) |
| `## System Prompt` | Custom system prompt appended to built-in guidance |
| `## Tools` | Allowlist of enabled tools; supports glob patterns for bash (e.g. `bash(go test *)`) |
| `## Tool Guidance` | Extra tool-use instructions injected into the system prompt |
| `## Tool Descriptions` | Custom descriptions injected into tool definitions |
| `## Sub-Agent Modes` | Named sub-agent personas (e.g. `### explore`, `### plan`) |
| `## Model Parameters` | Provider parameters (e.g. `seed: 42`) |
| `## Constraints` | `timeout` and `max_steps` per model |
| `## Context` | `max_history_turns`, `strategy`, `compact_threshold`, `task_mode` |
| `## Context Summarization Prompt` | Custom prompt for `/compact` and auto-compact |
| `## System Reminders` | Trigger-based messages injected mid-conversation |
| `## Hooks` | Shell commands triggered by tool events (e.g. run tests after edits) |
| `## Output Processing` | Per-tool output truncation rules |
| `## Model Profiles` | Per-model capability overrides (context budget, tool format, tier) |
| `## Sub-Agent` | Sub-agent model, max depth, and tool inheritance |
| `## Sandbox` | Filesystem and network restrictions |
| `## MCP Servers` | Additional MCP tool servers |
| `## Metadata` | Name, description, tags, author, project_root, extends |
| `## Tasks` | Task prompts for `errata run` (headless mode only) |
| `## Success Criteria` | Automated pass/fail checks (`no_errors`, `has_writes`) |

A full example with every section is available in `recipe.example.md`.

### Task modes (headless)

The `task_mode` field in `## Context` controls how tasks are executed:

- **`independent`** (default): Each task runs in isolation. All models are compared per task.
- **`sequential`**: Tasks run in order. The best model's writes are applied to disk before
  the next task starts, so later tasks build on earlier results.

---

## Preference log

Every selection is appended to `data/preferences.jsonl` (never overwritten):

```json
{
  "ts": "2026-02-21T10:00:00Z",
  "prompt_hash": "sha256:a3f...",
  "prompt_preview": "Read src/utils/retry.py and add exponential backoff...",
  "models": ["claude-sonnet-4-6", "gpt-4o"],
  "selected": "claude-sonnet-4-6",
  "latencies_ms": {"claude-sonnet-4-6": 891, "gpt-4o": 1243},
  "session_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"
}
```

The log is yours — query it with `jq`, load it into a dataframe, or feed it to another model.

---

## Running tests

```bash
go test ./...        # all packages
go test -v ./...     # verbose
make test            # same, via Makefile
```

---

## Building

```bash
make build           # ./errata for current platform
make build-all       # cross-compile darwin/linux/windows to dist/
make install         # go install to $GOPATH/bin
```

---

## Project layout

```
errata/
├── cmd/errata/
│   └── main.go                  # cobra entrypoint (errata, errata run, errata stats)
├── internal/
│   ├── adapters/
│   │   ├── registry.go          # NewAdapter(), ListAdapters() — routing by prefix/slash
│   │   ├── common.go            # DispatchTool, EmitSnapshot, Build*Response — shared helpers
│   │   ├── list.go              # ListAvailableModels() — per-provider model catalogue fetch
│   │   ├── anthropic.go         # AnthropicAdapter.RunAgent()
│   │   ├── openai.go            # OpenAIAdapter.RunAgent()
│   │   ├── gemini.go            # GeminiAdapter.RunAgent()
│   │   ├── openrouter.go        # OpenRouterAdapter — any model via "provider/model" IDs
│   │   ├── litellm.go           # LiteLLMAdapter — local/self-hosted proxy
│   │   ├── azure_openai.go      # AzureOpenAIAdapter — Azure-hosted OpenAI models
│   │   ├── bedrock.go           # BedrockAdapter — AWS Bedrock (Converse API)
│   │   └── vertex_ai.go         # VertexAIAdapter — Google Cloud Vertex AI
│   ├── capabilities/
│   │   └── defaults.go          # per-model capability defaults (context budget, tool format)
│   ├── checkpoint/
│   │   └── checkpoint.go        # Save/Load/Clear/Build/IncrementalSaver — /resume state
│   ├── commands/
│   │   └── commands.go          # canonical slash command registry
│   ├── config/
│   │   └── config.go            # Config struct, Load(), ResolvedActiveModels()
│   ├── criteria/
│   │   └── criteria.go          # success criteria evaluation (no_errors, has_writes)
│   ├── diff/
│   │   └── diff.go              # Compute() → FileDiff (Myers algorithm)
│   ├── headless/
│   │   ├── headless.go          # Run() — headless task runner for `errata run`
│   │   └── report.go            # RunReport, Save/Load JSON reports
│   ├── history/
│   │   └── history.go           # Load(), Save(), Clear() — conversation history
│   ├── hooks/
│   │   └── hooks.go             # recipe-defined hooks (post_tool_use, session_start)
│   ├── logging/
│   │   └── logger.go            # Logger, Wrap()/WrapAll() — per-run JSONL logging
│   ├── mcp/
│   │   ├── client.go            # JSON-RPC 2.0 stdio client (MCP protocol)
│   │   └── manager.go           # subprocess lifecycle, tool discovery, dispatcher
│   ├── models/
│   │   └── types.go             # ModelAdapter interface, AgentEvent, ModelResponse
│   ├── output/
│   │   └── output.go            # BuildReport, human-readable report formatting
│   ├── preferences/
│   │   └── preferences.go       # Record(), LoadAll(), Summarize(), SummarizeDetailed()
│   ├── pricing/
│   │   └── pricing.go           # LoadPricing(), CostUSD(), ContextWindowTokens()
│   ├── prompt/
│   │   └── assembler.go         # DefaultSummarizationPrompt, WithSummarizationPrompt(), ResolveSummarizationPrompt()
│   ├── prompthistory/
│   │   └── prompthistory.go     # prompt history persistence (Up-arrow / Ctrl-R)
│   ├── recipe/
│   │   └── recipe.go            # Recipe struct, Discover(), Parse(), Default(), ApplyTo(), MarshalMarkdown()
│   ├── reminders/
│   │   └── reminders.go         # trigger-based system reminders mid-conversation
│   ├── runner/
│   │   └── runner.go            # RunAll(), TrimHistory(), CompactHistories(), HasInterrupted()
│   ├── sandbox/
│   │   └── sandbox.go           # filesystem/network restrictions (platform-specific)
│   ├── subagent/
│   │   └── subagent.go          # sub-agent orchestration (spawn, dispatch, depth control)
│   ├── tooloutput/
│   │   └── process.go           # tool output processing (truncation rules)
│   ├── tools/
│   │   └── tools.go             # ToolDef, Definitions, Execute* functions, MCP helpers
│   ├── ui/
│   │   ├── app.go               # bubbletea program, mode state machine
│   │   ├── cmd_handlers.go      # slash command dispatch + export/import handlers
│   │   ├── complete.go          # tab completion and hint rendering (capped at 8 lines)
│   │   ├── config_panel.go      # /config overlay: sections, scalar/list/text editing
│   │   ├── diff.go              # diff + selection menu rendering
│   │   ├── input.go             # textarea input handling, prompt history
│   │   ├── mention.go           # @file mention expansion
│   │   ├── panels.go            # live agent panel rendering (lipgloss)
│   │   └── selection.go         # model selection UI
├── recipe.example.md                # full-featured recipe example (every section)
├── go.mod
├── go.sum
└── Makefile
```
