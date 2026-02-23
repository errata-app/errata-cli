# Errata

A/B testing tool for agentic AI models. Send a prompt to multiple models simultaneously,
watch each one read your files and propose changes live, pick the best proposal, and apply
it to disk. Every choice is logged so you can see which models you actually prefer over time.

---

## What it does

1. You type a prompt in the Errata REPL
2. All configured models receive it concurrently, each running as a coding agent
3. Models read your files on demand and propose file changes
4. Live panels show each model's tool activity (reading/writing) in real time
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
╭── claude-sonnet-4-6  running… ──╮  ╭── gpt-4o  running… ──────────╮
│ reading  src/utils/retry.py      │  │ reading  src/utils/retry.py   │
│ writing  src/utils/retry.py      │  │ writing  src/utils/retry.py   │
╰──────────────────────────────────╯  ╰───────────────────────────────╯
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
| `/clear` | Clear the display history |
| `/compact` | Summarize conversation history to free up context window |
| `/verbose` | Toggle verbose mode (model text alongside tool events) |
| `/models` | List currently active models |
| `/model <id> [id...]` | Restrict subsequent runs to specific model(s) |
| `/model` | Reset model filter — all configured models run again |
| `/exit` or `/quit` | Exit |
| `Ctrl-D` | Exit |

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

The env var sets the starting set of active models; `/model` overrides it for the session.

---

## Verbose mode

Toggle `/verbose` to also see each model's explanatory text alongside tool events. Verbose
mode is off by default in the TUI and on by default in the web UI.

---

## Context window management

Errata maintains a per-model conversation history across prompts within a session. Each
panel's status line shows the estimated context fill (e.g. `~42% ctx`) so you can see
how much of a model's window is in use.

Two mechanisms keep history from growing unbounded:

**Sliding window (automatic):** Only the most recent 20 turns are sent to the model on
each call. Set `ERRATA_MAX_HISTORY_TURNS` in `.env` to override.

**Compaction (manual or automatic):** `/compact` asks each model to write a concise
summary of the conversation so far, then replaces the full history with that summary.
This preserves continuity while freeing context. Compaction also triggers automatically
when a model's estimated history fill reaches 80%.

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
│   │   ├── anthropic.go     # AnthropicAdapter.RunAgent()
│   │   ├── openai.go        # OpenAIAdapter.RunAgent()
│   │   ├── gemini.go        # GeminiAdapter.RunAgent()
│   │   ├── openrouter.go    # OpenRouterAdapter — any model via "provider/model" IDs
│   │   └── litellm.go       # LiteLLMAdapter — local/self-hosted proxy
│   ├── pricing/
│   │   └── pricing.go       # LoadPricing(), CostUSD(), ContextWindowTokens() — OpenRouter fetch + fallback
│   ├── runner/
│   │   └── runner.go        # RunAll(), AppendHistory(), TrimHistory(), CompactHistories()
│   ├── tools/
│   │   └── tools.go         # FileWrite, tool schemas, ExecuteRead(), ApplyWrites()
│   ├── diff/
│   │   └── diff.go          # Compute() → FileDiff (Myers algorithm via sergi/go-diff)
│   ├── logging/
│   │   └── logger.go        # Logger, Wrap()/WrapAll() — per-run JSONL logging
│   ├── preferences/
│   │   └── preferences.go   # Record(), LoadAll(), Summarize()
│   ├── ui/
│   │   ├── app.go           # bubbletea program, mode state machine
│   │   ├── panels.go        # agent panel rendering (lipgloss)
│   │   ├── diff.go          # diff + selection menu rendering
│   │   └── keys.go          # key bindings
│   └── web/
│       ├── server.go        # Server struct, route registration, embedded static assets
│       ├── handlers.go      # WebSocket handler, REST handlers (/api/stats, /api/models)
│       └── static/
│           ├── index.html
│           ├── style.css
│           └── app.js
├── go.mod
├── go.sum
└── Makefile
```
