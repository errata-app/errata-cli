# Errata — A/B Testing Tool for Agentic AI Models

## Project Overview

**Errata** is a CLI tool that sends programming prompts to multiple AI models simultaneously,
runs each model as a coding agent with read/write file tools, shows live tool-event panels,
lets the user select the best proposal, and applies the winner's file writes to disk.
Every selection is logged for preference analysis over time.

> **Status:** Rewriting from Python to Go. The Python implementation in `src/` is the
> current working version. The Go implementation will replace it entirely.

---

## Target Stack (Go)

- **Language:** Go 1.23+
- **CLI framework:** `cobra` (subcommand routing, --help)
- **TUI:** `charmbracelet/bubbletea` + `charmbracelet/lipgloss` (replaces Rich + prompt_toolkit)
- **AI SDKs:** `anthropic-sdk-go`, `openai-go`, `google.golang.org/genai`
- **Config:** `joho/godotenv` + `os.Getenv` (replaces pydantic-settings)
- **Preferences:** append-only JSONL at `data/preferences.jsonl` (schema unchanged)
- **Distribution:** single static binary; cross-compiled via Makefile

## Current Stack (Python — to be replaced)

- **Language:** Python 3.11+
- **Package manager:** `uv`
- **CLI/TUI:** `rich` + `prompt_toolkit`
- **AI SDKs:** `anthropic`, `openai`, `google-generativeai`
- **Config:** `pydantic-settings` + `.env`

---

## Target Directory Structure (Go)

```
errata/
├── cmd/
│   └── errata/
│       └── main.go              # cobra root + subcommand wiring
├── internal/
│   ├── config/
│   │   └── config.go            # Config struct, Load(), ResolvedActiveModels()
│   ├── models/
│   │   ├── base.go              # ModelAdapter interface, AgentEvent, FileWrite, ModelResponse
│   │   ├── registry.go          # NewAdapter(), ListAdapters()
│   │   ├── anthropic.go         # AnthropicAdapter
│   │   ├── openai.go            # OpenAIAdapter
│   │   └── gemini.go            # GeminiAdapter
│   ├── runner/
│   │   └── runner.go            # RunAll() — goroutines + sync.WaitGroup
│   ├── tools/
│   │   └── tools.go             # Tool schemas, ExecuteRead(), ApplyWrites()
│   ├── diff/
│   │   └── diff.go              # ComputeDiff() → FileDiff (shared by TUI + future web)
│   ├── preferences/
│   │   └── preferences.go       # Record(), LoadAll(), Summarize()
│   └── ui/
│       ├── app.go               # bubbletea program, mode state machine
│       ├── panels.go            # agent panel rendering
│       ├── diff.go              # diff rendering
│       └── keys.go              # key bindings
├── go.mod
├── go.sum
├── Makefile
└── .env                         # (gitignored)
```

## Current Directory Structure (Python)

```
src/errata/
├── cli.py            # REPL loop, asyncio entrypoint
├── runner.py         # run_all() — asyncio.gather fan-out
├── display.py        # Rich Live panels, diff rendering, stats
├── preferences.py    # Append-only JSONL log + summarize()
├── config.py         # pydantic-settings — API keys, active models
├── tools.py          # Tool schemas, execute_read(), apply_writes()
└── models/
    ├── base.py       # ModelAdapter ABC, AgentEvent, FileWrite, ModelResponse
    ├── registry.py   # get_adapter() / list_adapters()
    ├── anthropic.py  # Claude agentic loop
    ├── openai.py     # GPT agentic loop
    └── gemini.py     # Gemini agentic loop
```

---

## Key Commands

### Go (target)
```bash
go build -o errata ./cmd/errata   # build binary
./errata                           # start TUI REPL
./errata stats                     # preference summary
./errata serve                     # web server (future)
make test                          # go test ./...
make lint                          # golangci-lint run ./...
make build-all                     # cross-compile all platforms
```

### Python (current)
```bash
uv sync --extra dev    # install deps
uv run errata          # start REPL
uv run errata stats    # preference summary
uv run pytest          # tests
uv run ruff check src tests  # lint
```

---

## REPL Slash Commands (unchanged across implementations)

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/verbose` | Toggle verbose mode (show model text output alongside tool events) |
| `/stats` | Preference win summary |
| `/models` | List currently active models |
| `/exit` or `/quit` | Exit |
| `Ctrl-D` | Exit |

---

## Core Workflow

1. User types a prompt in the Errata REPL
2. All configured models receive the prompt concurrently (goroutines / asyncio.gather)
3. Each model runs as an agent: reads files on demand, proposes writes (intercepted)
4. Live panels show tool events (reading/writing/error); `/verbose` adds text chunks
5. Once all models finish, a compact diff view shows what each model proposes to change
6. User picks a model by number; that model's proposed writes are applied to disk
7. Preference is recorded to `data/preferences.jsonl`

---

## Agentic Tool Loop

Each model adapter runs a multi-turn loop:
- `read_file` tool calls execute immediately (safe, read-only)
- `write_file` tool calls are **intercepted** — content stored as proposals, not written
- Loop exits when the model stops calling tools
- Proposed writes are applied only after the user selects that model

Timeout: 300 seconds per agent (multi-turn loops do more work than a single call).

---

## Model Configuration

```bash
# .env
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...

# Optional: pin specific models
ERRATA_ACTIVE_MODELS=claude-opus-4-6,claude-sonnet-4-6
```

Default models (one per provider, based on which keys are present):

| Provider  | Default model        |
|-----------|----------------------|
| Anthropic | `claude-sonnet-4-6`  |
| OpenAI    | `gpt-4o`             |
| Google    | `gemini-2.0-flash`   |

Model routing by ID prefix: `claude*` → Anthropic, `gpt-*`/`o1`/`o3` → OpenAI, `gemini*` → Google.

---

## Preference Schema (JSONL — unchanged)

```json
{
  "ts": "2026-02-21T10:00:00Z",
  "prompt_hash": "sha256:...",
  "prompt_preview": "first 120 chars...",
  "models": ["claude-sonnet-4-6", "gpt-4o"],
  "selected": "claude-sonnet-4-6",
  "latencies_ms": {"claude-sonnet-4-6": 1200, "gpt-4o": 800},
  "session_id": "uuid"
}
```

---

## Go Development Guidelines

- All rendering lives in `internal/ui/` — no lipgloss imports elsewhere
- Adapters implement `ModelAdapter` interface; tool schemas defined in `internal/tools/`
- Diff computation in `internal/diff/` — shared by TUI and future web server
- Preferences are append-only — never overwrite, only `O_APPEND`
- API keys from env only — never commit `.env`
- If a model's API key is missing, skip it with a printed warning (don't crash)
- Context cancellation propagates through all adapter calls
- Agent timeout: 5 minutes per adapter (`context.WithTimeout`)

## Python Development Guidelines (current)

- All model calls are async — use `asyncio` throughout
- All Rich rendering lives in `display.py` — no Rich imports elsewhere
- Preferences are append-only — never modify `preferences.jsonl`, only append
- Corrupt/unparseable JSONL lines are skipped with a warning, not raised
- API keys live in `.env` only — never commit them

---

## Testing

### Go (target)
```bash
go test ./...                          # all packages
go test ./internal/runner/...          # single package
go test -v ./...                       # verbose
```

Test files: `*_test.go` alongside source. Stub adapters implement `ModelAdapter`.
Table-driven tests preferred.

### Python (current)
```bash
uv run pytest          # all tests
uv run pytest -v       # verbose
```

---

## CI

GitHub Actions on every push/PR to `master`:
- **Go (target):** `go test ./...` + `golangci-lint run` on Go 1.23
- **Python (current):** `pytest -v` + `ruff check src tests` on Python 3.11 and 3.12

On `v*` tags: `make build-all` → upload platform binaries as GitHub release assets.

---

## Files to Never Commit

- `.env`
- `data/preferences.jsonl`
- `dist/` (compiled binaries)
