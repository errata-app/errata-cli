# Errata — A/B Testing Tool for Agentic AI Models

## Project Overview

**Errata** is a tool that sends programming prompts to multiple AI models simultaneously,
runs each as a coding agent with `read_file` / `write_file` tools, shows live tool-event panels,
lets the user select the best proposal, and applies the winner's file writes to disk.
Every selection is logged for preference analysis over time.

Two user surfaces share the same core engine:
- **TUI** (`./errata`) — bubbletea REPL for terminal use
- **Web** (`./errata serve`) — browser UI over WebSocket, persists history in localStorage

---

## Stack

- **Language:** Go 1.23+
- **CLI:** `github.com/spf13/cobra` — subcommand routing and `--help`
- **TUI:** `github.com/charmbracelet/bubbletea` + `github.com/charmbracelet/lipgloss`
- **Web:** `net/http` + `github.com/coder/websocket` (embedded static assets via `//go:embed`)
- **AI SDKs:** `anthropic-sdk-go v1.26`, `openai-go v1.12`, `google.golang.org/genai v1.47`
- **Config:** `github.com/joho/godotenv` + `os.Getenv`
- **Preferences:** append-only JSONL at `data/preferences.jsonl`
- **Run logs:** append-only JSONL at `data/log.jsonl` (via `internal/logging`)
- **Distribution:** single static binary; cross-compiled via `make build-all`

---

## Directory Structure

```
errata/
├── cmd/errata/
│   └── main.go              # cobra root (errata, errata stats, errata serve)
├── internal/
│   ├── config/
│   │   └── config.go        # Config struct, Load(), ResolvedActiveModels()
│   ├── models/
│   │   ├── base.go          # ModelAdapter interface, AgentEvent, ModelResponse
│   │   ├── registry.go      # NewAdapter(), ListAdapters() — prefix routing
│   │   ├── pricing.go       # pricingTable, CostUSD() — hardcoded $/M-token rates
│   │   ├── anthropic.go     # AnthropicAdapter.RunAgent()
│   │   ├── openai.go        # OpenAIAdapter.RunAgent()
│   │   └── gemini.go        # GeminiAdapter.RunAgent()
│   ├── runner/
│   │   └── runner.go        # RunAll() — goroutines + sync.WaitGroup
│   ├── tools/
│   │   └── tools.go         # FileWrite, ToolDef, ExecuteRead(), ApplyWrites()
│   ├── diff/
│   │   └── diff.go          # Compute() → FileDiff (LCS; shared by TUI + web)
│   ├── preferences/
│   │   └── preferences.go   # Record(), LoadAll(), Summarize()
│   ├── logging/
│   │   └── logger.go        # Logger, Wrap()/WrapAll() — per-run JSONL logging
│   ├── ui/
│   │   ├── app.go           # bubbletea program, mode state machine
│   │   ├── panels.go        # live agent panel rendering + fmtTokens()
│   │   ├── diff.go          # diff + selection menu rendering
│   │   └── keys.go          # key bindings
│   └── web/
│       ├── server.go        # Server struct, route registration, embedded static assets
│       ├── handlers.go      # WebSocket handler, REST handlers (/api/stats, /api/models)
│       └── static/
│           ├── index.html
│           ├── style.css
│           └── app.js
├── go.mod / go.sum
└── Makefile
```

---

## Key Commands

```bash
go build -o errata ./cmd/errata   # build binary
./errata                           # start TUI REPL
./errata serve                     # start web server (default :8080)
./errata serve --port 3000         # custom port
./errata stats                     # print preference summary (non-interactive)
make test                          # go test ./...
make lint                          # golangci-lint run ./...
make build-all                     # cross-compile darwin/linux/windows to dist/
```

---

## REPL Slash Commands

Both the TUI and the web textarea accept slash commands.

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/verbose` | Toggle verbose mode (model text alongside tool events) |
| `/models` | List currently active models (marks filter if set) |
| `/model <id> [id...]` | Restrict runs to specific model(s) — sticky until reset |
| `/model` | Reset model filter back to all configured models |
| `/exit` or `/quit` | Exit (TUI only) |
| `Ctrl-D` | Exit (TUI only) |

**Verbose mode** defaults to **off** in the TUI and **on** in the web UI (since the web is
designed for discussion and text responses are useful there).

---

## Core Workflow

1. User types a prompt (TUI REPL or web textarea)
2. `runner.RunAll()` fans out to all active adapters concurrently via goroutines
3. Each adapter runs a multi-turn agentic loop:
   - `read_file` calls execute immediately (path-traversal guarded, read-only)
   - `write_file` calls are **intercepted** — stored as proposals, not written to disk
   - Loop exits when the model stops calling tools
4. Live tool-event panels render while goroutines run
5. If no model proposed any file writes, responses are shown as text and the run ends
6. Otherwise a compact diff view shows each model's proposed changes
7. User selects a response; that model's `ProposedWrites` are applied via `tools.ApplyWrites()`
8. Preference entry appended to `data/preferences.jsonl`

Agent timeout: **5 minutes** per adapter (`context.WithTimeout`).

---

## Token Usage & Cost

Every adapter accumulates `InputTokens` and `OutputTokens` across all turns of its agentic
loop. `models.CostUSD(modelID, input, output)` looks up per-million-token rates from
`internal/models/pricing.go` and returns the estimated USD cost (0 for unknown model IDs).

These are surfaced in:
- **TUI panels** — `done  1234ms  ·  8.4k tok  ·  $0.0083` in the panel status line
- **TUI diff headers** — same stats in the `── model-id  …` section separator
- **TUI selection menu** — `(1234ms  $0.0083)` next to each option
- **Web diff headers and selection buttons** — same format

`pricing.go` must be updated manually when providers change their published rates.

---

## Model Filtering (`/model`)

Both surfaces maintain a per-session **active adapter filter** (nil = use all). The filter
is sticky — it persists across prompts until explicitly reset.

- `/model claude-sonnet-4-6` → only that adapter runs for subsequent prompts
- `/model claude-sonnet-4-6 gpt-4o` → two adapters run
- `/model` (bare) → reset to all configured adapters

Validation is **strict**: unknown model IDs are rejected immediately with the list of
available IDs. No changes take effect if any ID in the list is invalid.

**Implementation:** `App.activeAdapters` (TUI) and `activeAdapters` local var (web, per
connection). Both pass the filtered slice to `runner.RunAll`; only filtered panels are
created. The server-side WebSocket message type is `set_models`; client sends
`{type: "set_models", model_ids: [...]}`, server replies `{type: "models_set", models: [...]}`.

---

## Run Logging (`internal/logging`)

Every `RunAgent` call is logged to an append-only JSONL file (`data/log.jsonl` by default).
The `logging.Wrap` / `logging.WrapAll` functions return a `ModelAdapter` decorator that
intercepts `RunAgent`, collects all tool events, and appends a structured `Entry` after
the call returns. Pass `nil` to disable logging with zero overhead.

Log schema per line:
```json
{
  "ts": "...", "session_id": "...", "run_id": "...", "model_id": "...", "prompt": "...",
  "events": [{"type": "reading|writing|text|error", "data": "..."}],
  "response": {
    "text": "...", "input_tokens": 0, "output_tokens": 0, "cost_usd": 0.0,
    "latency_ms": 0, "proposed_files": ["..."],
    "writes": [{"path": "...", "content": "..."}], "error": ""
  }
}
```

---

## Web Architecture

The web server embeds all static assets at compile time (`//go:embed static`).
Each browser tab gets one persistent WebSocket connection; the server maintains
per-connection state (active adapter filter, last run results, cancel function).

### WebSocket message protocol

**Client → Server:**

| `type` | Fields | Description |
|--------|--------|-------------|
| `run` | `prompt`, `verbose` | Start a new agent run |
| `select` | `model_id` | Apply a model's proposed writes |
| `cancel` | — | Cancel the running agents |
| `set_models` | `model_ids` | Set model filter (empty = reset to all) |

**Server → Client:**

| `type` | Fields | Description |
|--------|--------|-------------|
| `agent_event` | `model_id`, `event_type`, `data` | Streaming tool event |
| `complete` | `responses[]` | All agents finished; payload includes diffs, tokens, cost |
| `applied` | `applied[]` | File writes applied successfully |
| `cancelled` | — | Run was cancelled |
| `models_set` | `models[]` | Confirms new active model filter |
| `error` | `message` | Server-side error |

### Web client state machine

```
idle → running → selecting → idle
                    ↓
                 (skip)
```

History is persisted to `localStorage` (capped at 50 entries). Completed runs are stored
as typed `{type:'run'}` entries that render as collapsible panels in the history view.

---

## Package Import Graph

```
tools       ← stdlib only
models      ← tools (for FileWrite, tool names, ExecuteRead/ApplyWrites)
config      ← stdlib only
runner      ← models, context, sync
diff        ← os, strings, sergi/go-diff
logging     ← models (ModelAdapter, ModelResponse), stdlib
preferences ← models (for ModelResponse latency/ID), encoding/json, os
ui          ← models, tools, runner, diff, bubbletea, lipgloss
web         ← models, runner, tools, diff, preferences, logging, coder/websocket
cmd/errata  ← config, models, preferences, logging, ui, web
```

**Critical:** `tools.FileWrite` lives in `internal/tools`, not `internal/models`.
This is intentional — moving it to `models` would create a cycle since adapters
(inside `models`) import `tools`, and `tools.ApplyWrites` needs `FileWrite`.

---

## Agentic Tool Loop Pattern

Each adapter (`anthropic.go`, `openai.go`, `gemini.go`) follows the same pattern:

```go
var totalInput, totalOutput int64
for {
    resp := callAPI(messages, tools)
    // accumulate token usage across turns:
    totalInput  += resp.Usage.InputTokens
    totalOutput += resp.Usage.OutputTokens
    for _, block := range resp.Content {
        if block is text  → collect text, optionally emit AgentEvent{Type:"text"}
        if block is tool_use:
            read_file  → ExecuteRead(), emit AgentEvent{Type:"reading"}, feed result back
            write_file → append to proposed[], emit AgentEvent{Type:"writing"}, ack "queued"
    }
    if no tool calls → break
    append tool results to messages, loop
}
return ModelResponse{
    ...,
    InputTokens:  totalInput,
    OutputTokens: totalOutput,
    CostUSD:      CostUSD(modelID, totalInput, totalOutput),
    ProposedWrites: proposed,
}
```

Tokens are accumulated across all turns (each turn re-sends context, so input grows).
Writes are **never** executed inside the loop — they accumulate in `proposed` and are
returned in `ModelResponse.ProposedWrites`.

---

## Provider SDK Notes

### Anthropic (`anthropic-sdk-go v1.26`)
- Response content: `[]ContentBlockUnion` — use `.AsText()`, `.AsToolUse()` to downcast
- Tool input is `json.RawMessage` on `ToolUseBlock.Input` — unmarshal manually
- Multi-turn: call `resp.ToParam()` to convert a response message back to a `MessageParam`
- Tool results: `anthropic.NewToolResultBlock(toolUseID, content, isError)` → `ContentBlockParamUnion`
- Tool definitions: `ToolUnionParam{OfTool: &ToolParam{...}}` with `ToolInputSchemaParam`
- `anthropic.String(s)` wraps a string in `param.Opt[string]` for optional fields
- Token usage: `resp.Usage.InputTokens` / `resp.Usage.OutputTokens` (both `int64`)

### OpenAI (`openai-go v1.12`)
- Convenience constructors: `openai.UserMessage(s)`, `openai.ToolMessage(content, toolCallID)`
- Multi-turn: `msg.ToParam()` converts a `ChatCompletionMessage` back to `ChatCompletionMessageParamUnion`
- Tool calls in response: `choice.Message.ToolCalls []ChatCompletionMessageToolCall`
- Function arguments are a JSON string: `json.Unmarshal([]byte(tc.Function.Arguments), &input)`
- Tool definitions: `ChatCompletionToolParam{Function: shared.FunctionDefinitionParam{...}}`
- `shared.FunctionParameters` is `map[string]any` — pass the full JSON schema object
- Token usage: `resp.Usage.PromptTokens` / `resp.Usage.CompletionTokens` (guard nil `resp.Usage`)

### Gemini (`google.golang.org/genai v1.47`)
- Use `genai.NewClient(ctx, &genai.ClientConfig{APIKey: key})`
- Tool-use loop via `client.Models.GenerateContent(ctx, modelID, contents, config)` with manually managed `[]*genai.Content` history
- Response parts: `resp.Candidates[0].Content.Parts` — check `part.Text != ""` and `part.FunctionCall != nil`
- `part.FunctionCall.Args` is already `map[string]any` — use `extractStringMap` to convert to `map[string]string`
- Tool results: `genai.NewPartFromFunctionResponse(name, map[string]any{"result": ...})`, appended as a user turn via `genai.NewContentFromParts(toolResults, genai.RoleUser)`
- Tool schemas: `&genai.Tool{FunctionDeclarations: []*genai.FunctionDeclaration{...}}`
  with `&genai.Schema{Type: genai.TypeObject, Properties: ..., Required: ...}`, passed in `GenerateContentConfig.Tools`
- Token usage: `resp.UsageMetadata.PromptTokenCount` / `resp.UsageMetadata.CandidatesTokenCount` (int32, guard nil)
- Model version: `resp.ModelVersion` (string) — populated in response, used as `ModelID` in `ModelResponse`

---

## TUI Architecture (bubbletea)

The TUI uses the Elm architecture: `Model → View`, `Update` on messages.

### Modes (state machine)
```
idle → running → selecting → idle
                    ↓
                 (skip / no writes)
```

- **idle**: `textarea` visible, slash commands handled, history shown
- **running**: agent panels rendered live; goroutines send `agentEventMsg` via `prog.Send()`
- **selecting**: diff view + numbered menu; key input collected character by character
- **no-writes shortcut**: if no adapter proposed any writes, skip selecting and return to idle

### Goroutine → TUI communication
```go
return a, func() tea.Msg {
    responses := runner.RunAll(ctx, adapters, prompt,
        func(modelID string, event models.AgentEvent) {
            prog.Send(agentEventMsg{modelID, event})  // safe from any goroutine
        }, verbose)
    return runCompleteMsg{responses}
}
```

`tea.Cmd` is returned from `Update` to start the goroutine; `program.Send()` injects
async results back into the event loop. This is the canonical bubbletea pattern for
long-running concurrent work.

---

## Diff Module

`internal/diff` uses `github.com/sergi/go-diff` (Myers algorithm, O(n) space) for line-level
diffs via `DiffLinesToRunes` → `DiffMainRunes` → `DiffCharsToLines`.
Output is a flat list of `Add / Remove / Context` lines, capped at `MaxDiffLines = 20`.
Hunk headers (`@@`) are omitted; instead, a "… N more lines" truncation notice is shown.
Used by both TUI (`internal/ui/diff.go`) and web (`internal/web/handlers.go`).

---

## Model Configuration

```bash
# .env
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...

# Optional: pin specific models (comma-separated)
ERRATA_ACTIVE_MODELS=claude-opus-4-6,claude-sonnet-4-6
```

Default models (one per provider, by available API key):

| Provider  | Default model        | ID prefix routing     |
|-----------|----------------------|-----------------------|
| Anthropic | `claude-sonnet-4-6`  | `claude*`             |
| OpenAI    | `gpt-4o`             | `gpt-*`, `o1`, `o3`   |
| Google    | `gemini-2.0-flash`   | `gemini*`             |

---

## Preference Schema (JSONL)

```json
{
  "ts": "2026-02-21T10:00:00Z",
  "prompt_hash": "sha256:...",
  "prompt_preview": "first 120 chars of prompt",
  "models": ["claude-sonnet-4-6", "gpt-4o"],
  "selected": "claude-sonnet-4-6",
  "latencies_ms": {"claude-sonnet-4-6": 891, "gpt-4o": 1243},
  "session_id": "hex-encoded-random-16-bytes"
}
```

Append-only. Corrupt lines are skipped with `log.Printf` (never crash on bad data).

---

## Development Guidelines

- All TUI rendering (lipgloss, panel layout, diff colors) lives in `internal/ui/` — no lipgloss imports elsewhere
- Tool schemas are defined once in `internal/tools/` and translated per-provider in each adapter
- `internal/diff/` has no external dependencies — keep it that way
- Preferences are append-only — always `O_APPEND|O_CREATE`, never truncate
- If a model's API key is missing, skip it with a warning; never crash on missing keys
- Context cancellation (`ctx.Done()`) propagates through all adapter API calls automatically
- Each adapter has a compile-time interface check: `var _ ModelAdapter = (*XAdapter)(nil)`
- `pricing.go` rates are hardcoded and must be updated manually — no runtime fetch

---

## Testing

```bash
go test ./...                  # all packages
go test -v ./internal/runner   # single package, verbose
go test -run TestExecuteRead   # single test
```

Test files: `*_test.go` alongside source in the same directory.
Stub adapters implement `ModelAdapter` in test files — no shared fixture infrastructure.
Table-driven tests preferred for config, preferences, and diff packages.

---

## Files to Never Commit

- `.env`
- `data/preferences.jsonl` (contains prompt history)
- `data/log.jsonl` (contains full prompt + response content)
- `dist/` (compiled binaries from `make build-all`)
