# Errata — A/B Testing Tool for Agentic AI Models

## Project Overview

**Errata** is a tool that sends programming prompts to multiple AI models simultaneously,
runs each as a coding agent with `read_file` / `write_file` tools, shows live tool-event panels,
lets the user select the best proposal, and applies the winner's file writes to disk.
Every selection is logged for preference analysis over time.

Two user surfaces share the same core engine:
- **TUI** (`./errata`) — bubbletea REPL for terminal use
- **Web** (`./errata serve`) — browser UI over WebSocket, persists conversation history to `data/history.json`

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
│   │   └── types.go         # ModelAdapter interface, AgentEvent, ModelResponse, ConversationTurn
│   ├── adapters/
│   │   ├── registry.go      # NewAdapter(), NewAdapterForProvider(), ListAdapters() — routing
│   │   ├── common.go        # DispatchTool, BuildErrorResponse, BuildSuccessResponse — shared helpers
│   │   ├── list.go          # ListAvailableModels(), ProviderModels — per-provider model catalogue fetch
│   │   ├── anthropic.go     # AnthropicAdapter.RunAgent()
│   │   ├── openai.go        # OpenAIAdapter.RunAgent()
│   │   ├── gemini.go        # GeminiAdapter.RunAgent()
│   │   ├── openrouter.go    # OpenRouterAdapter — OpenAI-compat, "provider/model" IDs
│   │   └── litellm.go       # LiteLLMAdapter — OpenAI-compat, "litellm/<model>" IDs
│   ├── pricing/
│   │   └── pricing.go       # LoadPricing(), CostUSD(), ContextWindowTokens() — OpenRouter fetch + hardcoded fallback
│   ├── runner/
│   │   └── runner.go        # RunAll(), AppendHistory(), TrimHistory(), CompactHistories()
│   ├── tools/
│   │   └── tools.go         # FileWrite, ToolDef, ExecuteRead(), ApplyWrites()
│   ├── diff/
│   │   └── diff.go          # Compute() → FileDiff (LCS; shared by TUI + web)
│   ├── preferences/
│   │   └── preferences.go   # Record(), LoadAll(), Summarize()
│   ├── history/
│   │   └── history.go       # Load(), Save(), Clear() — conversation history persistence
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
| `/compact` | Summarize conversation history to free up context window |
| `/models` | Query each configured provider for all available models; shows per-model pricing ($X in / $Y out /1M tokens); for OpenAI and Gemini shows only chat-capable models with a "N of M, chat only" count; caps display at 10 per provider with "… and N more" notice |
| `/stats` | Show preference win counts and per-model session cost |
| `/totalcost` | Show total inference cost accumulated this session |
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
loop. `pricing.CostUSD(qualifiedID, input, output)` looks up per-million-token rates and
returns the estimated USD cost (0 for unknown model IDs, gracefully omitted from UI).

**Pricing source:** `pricing.LoadPricing(cacheFile)` is called at startup. It fetches
`https://openrouter.ai/api/v1/models` (no auth required) and caches the result at
`data/pricing_cache.json` for 24 hours. Fallback chain:
1. Fresh cache (< 24 h) → use it
2. OpenRouter fetch succeeds → overwrite cache, use it
3. Stale cache → use it (log a warning)
4. No cache, fetch failed → fall back to hardcoded table in `pricing.go`

**Qualified IDs:** OpenRouter keys models as `provider/model`
(e.g. `anthropic/claude-sonnet-4-6`). Each native adapter passes its provider prefix
to `CostUSD` (e.g. `CostUSD("anthropic/"+modelID, ...)`). If the qualified key is
not found, `CostUSD` falls back to the bare portion after `/` for hardcoded-table
compatibility. OpenRouter and LiteLLM adapters pass their model ID as-is.

**`pricing.PricingFor(qualifiedID)`** returns `(inputPMT, outputPMT float64, ok bool)` using the
same qualified→bare fallback chain as `CostUSD`. Used by `/models` listing to display rates
alongside each model ID.

These are surfaced in:
- **TUI panels** — `done  1234ms  ·  8.4k tok  ·  $0.0083` in the panel status line
- **TUI diff headers** — same stats in the `── model-id  …` section separator
- **TUI selection menu** — `(1234ms  $0.0083)` next to each option
- **Web diff headers and selection buttons** — same format
- **`/models` listing** — `$X in / $Y out /1M` per model (both TUI and web)

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
`{type: "set_models", model_ids: [{id, provider}, ...]}` (elements are `ModelSpec` objects),
server replies `{type: "models_set", models: [...]}`. The `provider` field lets the server
route novel model IDs that don't match a known prefix via `adapters.NewAdapterForProvider`.

**Web persistence:** The web client saves the active model filter to `localStorage` under
`errata_model_filter` and restores it on reconnect by re-sending `set_models`.

---

## Context Window Management

Errata maintains a per-model `map[string][]ConversationTurn` conversation history across
prompts within a session. Each panel status line shows `~N% ctx` to indicate estimated
history fill relative to the model's known context window.

**Sliding window (automatic, in `RunAll`):**
`runner.TrimHistory` keeps the most recent `maxHistoryTurns` turns (default 20, rounded
to whole user+assistant pairs) before each API call. Override via `ERRATA_MAX_HISTORY_TURNS`.

**Compaction (manual + automatic):**
- `/compact` (both surfaces) calls `runner.CompactHistories`, which runs each adapter
  against its own history with a summarization prompt and replaces the history with a
  single `[user: "[Previous conversation — compacted]", assistant: <summary>]` pair.
- Auto-compact triggers in `RunAll` when `runner.ShouldAutoCompact` returns true
  (estimated history tokens / context window ≥ 80%). The run proceeds with the
  compacted history.
- Panel status shows `~N% ctx` based on `pricing.ContextWindowTokens(modelID)`.
  Returns 0 for unknown models (display suppressed).

**Context overflow recovery:** `runner.IsContextOverflowError` matches known
context-limit error strings from all providers. When detected, the TUI shows
`"context limit reached — use /clear or /compact to reset"`.

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

### REST endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/stats` | Preference tally JSON |
| `GET` | `/api/models` | Active model IDs JSON |
| `GET` | `/api/available-models` | Active models + all models from each configured provider (concurrent fetch, 15 s timeout); each provider entry includes `name`, `count`, `total_count` (pre-filter API count), `truncated` (models omitted past cap), and `models[]` — each entry has `id`, `input_pmt`, `output_pmt` (per-million-token USD, omitted when unknown); display capped at `ModelListCap=10` per provider |

### WebSocket message protocol

**Client → Server:**

| `type` | Fields | Description |
|--------|--------|-------------|
| `run` | `prompt`, `verbose` | Start a new agent run |
| `select` | `model_id` | Apply a model's proposed writes |
| `cancel` | — | Cancel the running agents |
| `set_models` | `model_ids` | Set model filter (empty = reset to all); elements are `{id, provider}` objects (`ModelSpec`); `provider` enables routing of novel IDs via `NewAdapterForProvider` |
| `compact` | — | Summarize conversation history to free context window |
| `clear_history` | — | Wipe server-side conversation history and delete `data/history.json` |

**Server → Client:**

| `type` | Fields | Description |
|--------|--------|-------------|
| `agent_event` | `model_id`, `event_type`, `data` | Streaming tool event |
| `complete` | `responses[]` | All agents finished; payload includes diffs, tokens, cost |
| `applied` | `applied[]` | File writes applied successfully |
| `cancelled` | — | Run was cancelled |
| `compact_complete` | — | History compaction finished |
| `history_cleared` | — | Confirms server-side history was wiped |
| `models_set` | `models[]` | Confirms new active model filter |
| `error` | `message` | Server-side error |

### Web client state machine

```
idle → running → selecting → idle
                    ↓
                 (skip)
```

**Connection lifecycle:** The textarea and Send button start `disabled` with placeholder
"Connecting…" in the HTML. `ws.onopen` enables them and restores the normal placeholder.
`ws.onclose` disables them again with "Reconnecting…". `toIdle()` only re-enables inputs
when `ws.readyState === WebSocket.OPEN`, preventing the hang-on-reconnect race condition.

**Display history** is persisted to `localStorage` (capped at 50 entries). Completed runs
are stored as typed `{type:'run'}` entries that render as collapsible panels in the history view.

**Conversation history** (the per-model `[]ConversationTurn` sent to the AI on each prompt)
is stored server-side in `Server.histories` and persisted to `data/history.json` after every
run and compact. All browser tabs share the same history. Reconnecting picks up where the
previous connection left off. `/clear` sends `clear_history` to the server and wipes both
the display history (localStorage) and the conversation history (disk).

---

## Package Import Graph

```
tools       ← stdlib only
pricing     ← stdlib only
models      ← tools (for FileWrite, tool names, ExecuteRead/ApplyWrites)
config      ← stdlib only
adapters    ← models, pricing, tools, config, provider SDKs
runner      ← models, pricing
diff        ← os, strings, sergi/go-diff
history     ← models, encoding/json, os
logging     ← models (ModelAdapter, ModelResponse), stdlib
preferences ← models (for ModelResponse latency/ID), encoding/json, os
ui          ← models, pricing, tools, runner, diff, history, adapters, config, bubbletea, lipgloss
web         ← models, runner, tools, diff, preferences, logging, history, adapters, config, coder/websocket
cmd/errata  ← config, adapters, pricing, logging, ui, web
```

**Critical:** `tools.FileWrite` lives in `internal/tools`, not `internal/models`.
This is intentional — moving it to `models` would create a cycle since adapters
import `tools`, and `tools.ApplyWrites` needs `FileWrite`.

**Critical:** Never import `adapters` from within `models`, `pricing`, `runner`, `tools`,
`logging`, `diff`, or `preferences` — these packages sit below `adapters` in the import
graph and must remain adapter-agnostic.

---

## Agentic Tool Loop Pattern

Each adapter (`anthropic.go`, `openai.go`, `gemini.go`, `openrouter.go`, `litellm.go`)
follows the same pattern:

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
    InputTokens:  totalInput,
    OutputTokens: totalOutput,
    // native adapters prefix their provider; OpenRouter/LiteLLM pass ID as-is:
    CostUSD:      CostUSD("anthropic/"+modelID, totalInput, totalOutput),
    ProposedWrites: proposed,
}
```

Tokens are accumulated across all turns (each turn re-sends context, so input grows).
Writes are **never** executed inside the loop — they accumulate in `proposed` and are
returned in `ModelResponse.ProposedWrites`.

**`ModelID` is enforced by the runner:** `runner.RunAll` overwrites `resp.ModelID = a.ID()`
after every `RunAgent` call. Adapters do not need to set it. Provider SDKs return resolved
names like `gpt-4o-2024-08-06`; the runner normalises back to the configured ID so all UI
panel lookups and preference recording work correctly.

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

# OpenRouter — access any model via a single API key
OPENROUTER_API_KEY=sk-or-...

# LiteLLM — self-hosted proxy; base URL must include /v1
LITELLM_BASE_URL=http://localhost:4000/v1
LITELLM_API_KEY=optional

# Optional: pin specific models (comma-separated)
ERRATA_ACTIVE_MODELS=claude-opus-4-6,anthropic/claude-sonnet-4-6
```

Default models (auto-detected from available API keys; native providers only):

| Provider  | Default model        | ID routing rule              |
|-----------|----------------------|------------------------------|
| Anthropic | `claude-sonnet-4-6`  | prefix `claude`              |
| OpenAI    | `gpt-4o`             | prefix `gpt-`, `o1`, `o3`    |
| Google    | `gemini-2.0-flash`   | prefix `gemini`              |
| OpenRouter | _(none; must set `ERRATA_ACTIVE_MODELS`)_ | contains `/` |
| LiteLLM   | _(none; must set `ERRATA_ACTIVE_MODELS`)_ | prefix `litellm/` |

**OpenRouter** model IDs use `provider/model` format (e.g. `anthropic/claude-sonnet-4-6`,
`meta-llama/llama-3-70b-instruct`). Any model ID containing `/` routes to OpenRouter.

**LiteLLM** model IDs use the `litellm/` prefix (e.g. `litellm/claude-sonnet-4-6`).
The prefix is stripped before the API call; it remains in the display name.
`litellm/` must come before the `/` routing check in the registry to take precedence.

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
- `pricingTable` in `pricing.go` is the last-resort hardcoded fallback; the runtime source is the OpenRouter fetch cached at `data/pricing_cache.json`

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

**Testing requirements for every change:**
- Any new function or package must have accompanying tests.
- Any bug fix must include a regression test that would have caught the original bug.
- Any struct that is serialized to disk (JSON, JSONL) must have a round-trip test
  (write → read → assert values) to catch unexported-field and missing-json-tag bugs.
- Run `go test ./...` before considering a task complete.

---

## Files to Never Commit

- `.env`
- `data/preferences.jsonl` (contains prompt history)
- `data/log.jsonl` (contains full prompt + response content)
- `data/history.json` (contains full conversation context)
- `dist/` (compiled binaries from `make build-all`)
