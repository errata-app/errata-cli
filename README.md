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
# .env

# Native providers — auto-detected; one default model per available key
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...

# OpenRouter — single key for any model from any provider
OPENROUTER_API_KEY=sk-or-...

# LiteLLM — self-hosted proxy (base URL must include /v1)
LITELLM_BASE_URL=http://localhost:4000/v1
LITELLM_API_KEY=optional
```

Errata auto-detects native providers from available keys:

| Provider  | Default model         |
|-----------|-----------------------|
| Anthropic | `claude-sonnet-4-6`   |
| OpenAI    | `gpt-4o`              |
| Google    | `gemini-2.0-flash`    |

OpenRouter and LiteLLM models must be listed explicitly in `ERRATA_ACTIVE_MODELS`.

---

## Usage

### TUI (terminal REPL)

```bash
./errata
```

### Web UI

```bash
./errata serve           # starts on :8080
./errata serve --port 3000
```

Open `http://localhost:8080` in your browser. The web UI is functionally identical to the
TUI and shares the same WebSocket-based backend.

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
| `/clear` | Clear display history and wipe conversation context |
| `/compact` | Summarize conversation history to free up context window |
| `/verbose` | Toggle verbose mode (model text alongside tool events) |
| `/models` | List all available models from each configured provider with per-model pricing |
| `/tools` | Show current tool status (`on`/`off`); MCP tools are labelled `(mcp)` |
| `/tools off <name...>` | Disable one or more tools for this session (e.g. `/tools off bash`) |
| `/tools on <name...>` | Re-enable specific tools |
| `/tools reset` | Re-enable all tools |
| `/stats` | Show preference win counts and per-model session cost |
| `/totalcost` | Show total inference cost accumulated this session |
| `/model <id> [id...]` | Restrict subsequent runs to specific model(s) |
| `/model` | Reset model filter — all configured models run again |
| `/exit` or `/quit` | Exit |
| `Ctrl-D` | Exit |

**TUI input shortcuts:**

| Key | Action |
|-----|--------|
| `↑` (line 0) | Recall previous prompt (cycle backward through history) |
| `↓` (while navigating) | Cycle forward; at newest restores original typed input |
| `Ctrl-R` | Open reverse-i-search: type a substring to filter history; `Ctrl-R` again for next match; `Enter` to select; `Escape` to cancel |
| `Tab` | Complete the current `/command` prefix |
| `PgUp` / `PgDn` | Scroll the conversation feed |

---

## Model filtering

You can narrow which models run without restarting Errata. The filter is sticky — it
persists across prompts until explicitly reset.

**At runtime (both TUI and web):**

```
/model claude-sonnet-4-6          # only Claude for the next runs
/model claude-sonnet-4-6 gpt-4o   # two models
/model                            # reset — all configured models run again
```

Unknown model IDs are rejected immediately with a list of valid options.

**Statically via environment variable:**

```bash
# .env
# Native models
ERRATA_ACTIVE_MODELS=claude-opus-4-6,claude-sonnet-4-6

# OpenRouter models — use "provider/model" format
ERRATA_ACTIVE_MODELS=anthropic/claude-sonnet-4-6,openai/gpt-4o,meta-llama/llama-3-70b-instruct

# LiteLLM models — use "litellm/<model>" format
ERRATA_ACTIVE_MODELS=litellm/claude-sonnet-4-6,litellm/gpt-4o

# Mix native and OpenRouter
ERRATA_ACTIVE_MODELS=claude-sonnet-4-6,anthropic/claude-opus-4-6
```

---

## Verbose mode

Toggle `/verbose` to also see each model's explanatory text alongside tool events. Verbose
mode is off by default in the TUI and on by default in the web UI.

---

## Context window management

Errata maintains a per-model conversation history across prompts. History is saved to
`data/history.json` after every run so you can close the client and pick up exactly
where you left off. Use `/clear` to deliberately wipe it. Each panel's status line shows
the estimated context fill (e.g. `~42% ctx`) so you can see how much of a model's window
is in use.

Two mechanisms keep history from growing unbounded:

**Sliding window (automatic):** Only the most recent 20 turns are sent to the model on
each call. Set `ERRATA_MAX_HISTORY_TURNS` in `.env` to override.

**Compaction (manual or automatic):** `/compact` asks each model to write a concise
summary of the conversation so far, then replaces the full history with that summary.
This preserves continuity while freeing context. Compaction also triggers automatically
when a model's estimated history fill reaches 80%.

---

## MCP tool servers

Errata supports the [Model Context Protocol](https://modelcontextprotocol.io/) (MCP),
which lets you connect any MCP-compatible tool server (web search, code execution,
databases, etc.) and expose its tools to every model in the comparison harness.

### Configuration

```bash
# .env
# Format: name:command arg1 arg2,...  (comma-separated for multiple servers)
ERRATA_MCP_SERVERS=exa:npx @exa-ai/exa-mcp-server
```

Multiple servers:

```bash
ERRATA_MCP_SERVERS=exa:npx @exa-ai/exa-mcp-server,search:npx @modelcontextprotocol/server-brave-search
```

Each server is launched as a subprocess using stdio transport (the standard MCP deployment
model). Errata performs the MCP handshake at startup, discovers all tools the server
exposes, and registers them alongside the nine built-in tools.

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
- **Web UI:** warning delivered to the browser as an error message immediately on connect

Runtime errors during a tool call (e.g. the MCP server crashes mid-run) are surfaced in
the live panel as an error event so you can see the failure in context.

### Managing MCP tools at runtime

MCP tools appear in `/tools` listings labelled `(mcp)` and can be toggled like built-in tools:

```
/tools                     # list all tools including MCP
/tools off exa_search      # disable a specific MCP tool for this session
/tools on  exa_search      # re-enable it
/tools reset               # re-enable everything
```

The web UI `GET /api/tools` endpoint returns the full tool list with `"source": "builtin"` or
`"source": "mcp"` for each entry.

---

## Deployment configuration

Errata exposes several environment variables for fine-tuning the harness to match your
workflow or deployment environment.

### Custom system prompt

Append instructions to every model's system prompt without modifying source code:

```bash
# .env
ERRATA_SYSTEM_PROMPT="This project uses Python 3.11 and pytest. Always run pytest before proposing changes. Follow PEP 8 strictly."
```

The extra text is appended after the built-in tool guidance in each model's system prompt.
Use this for:
- Project-specific coding conventions
- Domain knowledge (e.g. "this is a financial system — never log PII")
- Workflow constraints (e.g. "always write tests before implementation")

### Tool management

```bash
# Disable bash execution for all sessions (can still be toggled at runtime)
# Not yet a startup flag — use /tools off bash at the REPL
```

Tools can always be toggled per-session with `/tools off <name>` and `/tools on <name>`.

### Model pinning

```bash
ERRATA_ACTIVE_MODELS=claude-opus-4-6,gpt-4o   # only these two models
```

### History and preferences paths

```bash
ERRATA_HISTORY_PATH=~/.errata/history.json          # default: data/history.json
ERRATA_PREFERENCES_PATH=~/.errata/preferences.jsonl # default: data/preferences.jsonl
```

### Debug logging

```bash
ERRATA_DEBUG_LOG=data/log.jsonl   # append-only JSONL with full prompt/response content
```

Each log entry includes the model ID, session ID, all tool events, token counts, latency,
and cost. Useful for auditing or building fine-tuning datasets.

### Context window

```bash
ERRATA_MAX_HISTORY_TURNS=20   # default; reduce for smaller context windows
```

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
│   └── main.go              # cobra entrypoint (errata, errata stats, errata serve)
├── internal/
│   ├── config/
│   │   └── config.go        # Config struct, Load(), ResolvedActiveModels()
│   ├── models/
│   │   └── types.go         # ModelAdapter interface, AgentEvent, ModelResponse, ConversationTurn
│   ├── adapters/
│   │   ├── registry.go      # NewAdapter(), ListAdapters() — routing by prefix/slash
│   │   ├── common.go        # DispatchTool, BuildErrorResponse, BuildSuccessResponse
│   │   ├── anthropic.go     # AnthropicAdapter.RunAgent()
│   │   ├── openai.go        # OpenAIAdapter.RunAgent()
│   │   ├── gemini.go        # GeminiAdapter.RunAgent()
│   │   ├── openrouter.go    # OpenRouterAdapter — any model via "provider/model" IDs
│   │   └── litellm.go       # LiteLLMAdapter — local/self-hosted proxy
│   ├── mcp/
│   │   ├── client.go        # JSON-RPC 2.0 stdio client (MCP protocol)
│   │   └── manager.go       # subprocess lifecycle, tool discovery, dispatcher registry
│   ├── pricing/
│   │   └── pricing.go       # LoadPricing(), CostUSD(), ContextWindowTokens()
│   ├── runner/
│   │   └── runner.go        # RunAll(), AppendHistory(), TrimHistory(), CompactHistories()
│   ├── tools/
│   │   └── tools.go         # ToolDef, Definitions, Execute* functions, MCP context helpers
│   ├── diff/
│   │   └── diff.go          # Compute() → FileDiff (Myers algorithm via sergi/go-diff)
│   ├── history/
│   │   └── history.go       # Load(), Save(), Clear() — conversation history persistence
│   ├── logging/
│   │   └── logger.go        # Logger, Wrap()/WrapAll() — per-run JSONL logging
│   ├── preferences/
│   │   └── preferences.go   # Record(), LoadAll(), Summarize()
│   ├── commands/
│   │   └── commands.go      # canonical slash command registry (TUI + web)
│   ├── prompthistory/
│   │   └── prompthistory.go # prompt history persistence (Up-arrow / Ctrl-R)
│   ├── ui/
│   │   ├── app.go           # bubbletea program, mode state machine
│   │   ├── panels.go        # agent panel rendering (lipgloss)
│   │   └── diff.go          # diff + selection menu rendering
│   └── web/
│       ├── server.go        # Server struct, route registration, embedded static assets
│       ├── handlers.go      # WebSocket handler, REST handlers
│       └── static/
│           ├── index.html
│           ├── style.css
│           └── app.js
├── go.mod
├── go.sum
└── Makefile
```
