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
- At least one API key: Anthropic, OpenAI, or Google

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
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...
```

Errata auto-detects which models to run based on which keys are present:

| Provider  | Default model         |
|-----------|-----------------------|
| Anthropic | `claude-sonnet-4-6`   |
| OpenAI    | `gpt-4o`              |
| Google    | `gemini-2.0-flash`    |

---

## Usage

### Start the REPL

```bash
./errata
```

### Run a prompt

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

Once all models finish, a diff view shows exactly what each proposed:

```
── claude-sonnet-4-6  891ms ───────────────────────────────
    src/utils/retry.py  +12 -3
    + def retry(max_attempts=3, backoff=2.0):
    -     time.sleep(1)
    +     time.sleep(backoff ** attempt)
    … 4 more lines

── gpt-4o  1243ms ─────────────────────────────────────────
    src/utils/retry.py  +8 -1
    +     delay = min(base * 2 ** attempt, max_delay)
```

Then the selection prompt:

```
Select a response to apply:
  1  claude-sonnet-4-6             (891ms)   →  src/utils/retry.py
  2  gpt-4o                        (1243ms)  →  src/utils/retry.py
  s  Skip

choice>
```

Pick a number — that model's writes are applied to disk immediately.

### Verbose mode

Toggle `/verbose` to also see each model's explanatory text alongside tool events.

---

## REPL commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/verbose` | Toggle verbose mode (model text alongside tool events) |
| `/models` | List currently active models |
| `/exit` or `/quit` | Exit |
| `Ctrl-D` | Exit |

---

## Pinning models

Override which models run via `ERRATA_ACTIVE_MODELS` in your `.env`:

```bash
ERRATA_ACTIVE_MODELS=claude-opus-4-6,claude-sonnet-4-6
```

Any model ID whose prefix matches a configured provider can be used here.

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
│   │   ├── base.go          # ModelAdapter interface, AgentEvent, ModelResponse
│   │   ├── registry.go      # NewAdapter(), ListAdapters() — prefix routing
│   │   ├── anthropic.go     # AnthropicAdapter.RunAgent()
│   │   ├── openai.go        # OpenAIAdapter.RunAgent()
│   │   └── gemini.go        # GeminiAdapter.RunAgent()
│   ├── runner/
│   │   └── runner.go        # RunAll() — goroutines + sync.WaitGroup
│   ├── tools/
│   │   └── tools.go         # FileWrite, tool schemas, ExecuteRead(), ApplyWrites()
│   ├── diff/
│   │   └── diff.go          # Compute() → FileDiff (LCS-based, no external library)
│   ├── preferences/
│   │   └── preferences.go   # Record(), LoadAll(), Summarize()
│   └── ui/
│       ├── app.go           # bubbletea program, mode state machine
│       ├── panels.go        # agent panel rendering (lipgloss)
│       ├── diff.go          # diff + selection menu rendering
│       └── keys.go          # key bindings
├── go.mod
├── go.sum
└── Makefile
```
