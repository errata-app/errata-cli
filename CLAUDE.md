# Errata — A/B Testing Tool for Agentic AI Models

## Project Overview

**Errata** is a tool that sends programming prompts to multiple AI models simultaneously,
runs each as a full coding agent with nine built-in tools plus any dynamically-registered MCP
tools, shows live tool-event panels, lets the user select the best proposal, and applies the
winner's file writes to disk. Every selection is logged for preference analysis over time.

This is a Go-primary project. Default to Go idioms, conventions, and tooling (`go test`, `go vet`,
`gofmt`) unless otherwise specified.

---

## Stack

- **Language:** Go 1.23+
- **CLI:** `github.com/spf13/cobra` — subcommand routing and `--help`
- **TUI:** `github.com/charmbracelet/bubbletea` + `github.com/charmbracelet/lipgloss`
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
│   └── main.go              # cobra root (errata, errata stats, errata run)
├── internal/
│   ├── config/
│   │   └── config.go        # Config struct, Load(), ResolvedActiveModels()
│   ├── models/
│   │   └── types.go         # ModelAdapter interface, AgentEvent, ModelResponse, ConversationTurn
│   ├── adapters/
│   │   ├── registry.go      # NewAdapter(), NewAdapterForProvider(), ListAdapters() — routing
│   │   ├── common.go        # DispatchTool, EmitSnapshot, BuildErrorResponse, BuildInterruptedResponse, BuildSuccessResponse — shared helpers
│   │   ├── list.go          # ListAvailableModels(), ProviderModels — per-provider model catalogue fetch
│   │   ├── anthropic.go     # AnthropicAdapter.RunAgent()
│   │   ├── openai.go        # OpenAIAdapter.RunAgent()
│   │   ├── gemini.go        # GeminiAdapter.RunAgent()
│   │   ├── openrouter.go    # OpenRouterAdapter — OpenAI-compat, "provider/model" IDs
│   │   └── litellm.go       # LiteLLMAdapter — OpenAI-compat, "litellm/<model>" IDs
│   ├── pricing/
│   │   └── pricing.go       # LoadPricing(), CostUSD(), ContextWindowTokens() — OpenRouter fetch + hardcoded fallback
│   ├── runner/
│   │   └── runner.go        # RunAll(), AppendHistory(), TrimHistory(), CompactHistories(), HasInterrupted()
│   ├── mcp/
│   │   ├── client.go        # JSON-RPC 2.0 stdio client (Content-Length framing)
│   │   └── manager.go       # MCP server subprocess lifecycle, tool discovery, dispatcher registry
│   ├── tools/
│   │   └── tools.go         # FileWrite, ToolDef, ExecuteRead(), ApplyWrites(), FilterDefs(), SetSystemPromptExtra()
│   ├── diff/
│   │   └── diff.go          # Compute() → FileDiff (LCS)
│   ├── preferences/
│   │   └── preferences.go   # Record(), LoadAll(), Summarize()
│   ├── history/
│   │   └── history.go       # Load(), Save(), Clear() — conversation history persistence
│   ├── logging/
│   │   └── logger.go        # Logger, Wrap()/WrapAll() — per-run JSONL logging
│   ├── checkpoint/
│   │   └── checkpoint.go    # Save(), Load(), Clear(), Build(), IncrementalSaver — interrupted run state persistence
│   ├── commands/
│   │   └── commands.go      # Command{Name,Desc}; All — canonical slash command registry
│   ├── prompthistory/
│   │   └── prompthistory.go # Load(), Append() — prompt history JSONL persistence
│   ├── ui/
│   │   ├── app.go           # bubbletea program, mode state machine
│   │   ├── cmd_handlers.go  # slash command dispatch and handlers
│   │   ├── config_panel.go  # /config overlay: sections, scalar/list/text editing
│   │   ├── complete.go      # tab completion and hint rendering (capped at 8 lines)
│   │   ├── panels.go        # live agent panel rendering + fmtTokens()
│   │   └── diff.go          # diff + selection menu rendering
├── go.mod / go.sum
└── Makefile
```

---

## Key Commands

```bash
go build -o errata ./cmd/errata            # build binary
./errata                                    # start TUI REPL
./errata stats                              # print preference summary (non-interactive)
./errata --debug-log data/log.jsonl         # enable JSONL debug logging
./errata -r myrecipe.md                     # use explicit recipe file
make test                                   # go test ./...
make lint                                   # golangci-lint run ./...
make build-all                              # cross-compile darwin/linux/windows to dist/
```

---

## REPL Slash Commands

The TUI REPL accepts slash commands.

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/clear` | Clear display (preserves conversation context) |
| `/wipe` | Wipe display and conversation memory |
| `/verbose` | Toggle verbose mode (model text alongside tool events) |
| `/compact` | Summarize conversation history to free up context window |
| `/models` | Query each configured provider for all available models; shows per-model pricing ($X in / $Y out /1M tokens); for OpenAI and Gemini shows only chat-capable models with a "N of M, chat only" count; caps display at 10 per provider with "… and N more" notice |
| `/tools` | Show current tool status (`on`/`off` for each tool, including any active MCP tools) |
| `/tools off <name...>` | Disable one or more tools for this session — works for both built-in and MCP tools |
| `/tools on <name...>` | Re-enable specific tools |
| `/tools reset` | Re-enable all tools |
| `/stats` | Show preference win counts and per-model session cost |
| `/totalcost` | Show total inference cost accumulated this session |
| `/model <id> [id...]` | Restrict runs to specific model(s) — sticky until reset |
| `/model` | Reset model filter back to all configured models |
| `/config` | View/edit configuration; `/config <section>` jumps to section; inline text editing with Ctrl+S to save |
| `/set <path> [value]` | Get or set a config path (e.g. `/set constraints.timeout 10m`); bare path queries current value |
| `/seed <n>` | Set seed for reproducibility; bare `/seed` clears |
| `/subset <id...>` | Target specific model(s); bare `/subset` shows current |
| `/all` | Reset to all models |
| `/remind [name]` | Fire a named reminder; bare `/remind` lists available |
| `/export recipe [path]` | Export the current session recipe to Markdown (default: `recipe_export.md`) |
| `/export output [path]` | Export the latest run's output report (default: `data/outputs/`) |
| `/import recipe <path>` | Import a recipe file, replacing the current session recipe |
| `/resume` | Resume an interrupted run — re-runs only the interrupted models, preserving completed results |
| `/exit` or `/quit` | Exit (TUI only) |
| `Ctrl-D` | Exit (TUI only) |

**Verbose mode** defaults to **off**.

**TUI prompt history** (Up-arrow cycling and Ctrl-R search):
- `↑` on the first textarea line → cycle backward through previous prompts
- `↓` while navigating → cycle forward; at newest, restores the original typed text
- `Ctrl-R` → opens `(reverse-i-search: "query"): <preview>` overlay above the textarea; typing narrows the match; `Ctrl-R` again cycles to the next older match; `Enter` loads the result; `Escape` dismisses
- Prompt history is persisted to `data/prompt_history.jsonl` so it survives restarts
- Only real AI prompts (not slash commands) are recorded

The canonical command list is defined in `internal/commands/commands.go` (`commands.All`).

---

## Core Workflow

1. User types a prompt (TUI REPL)
2. `runner.RunAll()` fans out to all active adapters concurrently via goroutines
3. Each adapter runs a multi-turn agentic loop using the active tool set:

   **Built-in tools (always available):**
   - `list_directory(path, depth)` — directory tree (read-only, path-traversal guarded)
   - `search_files(pattern, base_path)` — glob file search with `**` support (read-only)
   - `search_code(pattern, path, file_glob)` — regex content search via `grep -rn` (read-only)
   - `read_file(path, offset, limit)` — file contents with pagination (read-only, path-traversal guarded)
   - `edit_file(path, old_string, new_string)` — exact-string-replace; **intercepted** like write_file
   - `bash(command, description)` — shell execution with 2-minute timeout
   - `write_file(path, content)` — **intercepted**: stored as proposals, not written to disk
   - `web_fetch(url)` — fetch and clean a public URL (HTML stripped, 50 KB cap)
   - `web_search(query)` — DuckDuckGo instant answers (knowledge panels; no API key required)

   **MCP tools (dynamically registered at startup):**
   - Any tool exposed by servers in recipe `## MCP Servers` — injected into the same dispatch table
   - Models see and can call MCP tools identically to built-in tools
   - Loop exits when the model stops calling tools

4. Active tool set is configurable per-session with `/tools off <name>` / `/tools on <name>` / `/tools reset` — works for both built-in and MCP tools
5. Live tool-event panels render while goroutines run
6. If no model proposed any file writes, responses are shown as text and the run ends
7. Otherwise a compact diff view shows each model's proposed changes
8. User selects a response; that model's `ProposedWrites` are applied via `tools.ApplyWrites()`
9. Preference entry appended to `data/preferences.jsonl`

Agent timeout: **5 minutes** per adapter (`context.WithTimeout`).

**Interruption:** Users can cancel a running prompt with ESC or Ctrl-C (TUI)
or SIGINT/SIGTERM (headless). Cancellation propagates via `context.WithCancel`
through all active adapter API calls. Partial results (accumulated text, proposed writes,
token counts) are preserved in the response with `Interrupted: true`. A checkpoint is saved
to `data/checkpoint.json` so the run can be resumed with `/resume`.

**Incremental checkpointing (crash resilience):** In addition to the post-run checkpoint,
each adapter emits a `"snapshot"` `AgentEvent` at every turn boundary (after tool dispatch,
before the next API call). The runner intercepts these via `checkpoint.IncrementalSaver`,
which atomically flushes per-model state to `data/checkpoint.json` on each update. This
ensures partial work survives ungraceful termination (SIGKILL, OOM kill, power loss) —
the checkpoint file always reflects the most recent complete turn for each model.

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
- **`/models` listing** — `$X in / $Y out /1M` per model

---

## Model Filtering (`/model`)

The TUI maintains a per-session **active adapter filter** (nil = use all). The filter
is sticky — it persists across prompts until explicitly reset.

- `/model claude-sonnet-4-6` → only that adapter runs for subsequent prompts
- `/model claude-sonnet-4-6 gpt-4o` → two adapters run
- `/model` (bare) → reset to all configured adapters

Validation is **strict**: unknown model IDs are rejected immediately with the list of
available IDs. No changes take effect if any ID in the list is invalid.

**Implementation:** `App.activeAdapters` passes the filtered slice to `runner.RunAll`;
only filtered panels are created.

---

## Context Window Management

Errata maintains a per-model `map[string][]ConversationTurn` conversation history across
prompts within a session. Each panel status line shows `~N% ctx` to indicate estimated
history fill relative to the model's known context window.

**Sliding window (automatic, in `RunAll`):**
`runner.TrimHistory` keeps the most recent `maxHistoryTurns` turns (default 20, rounded
to whole user+assistant pairs) before each API call. Override via recipe `## Context` `max_history_turns:`.

**Compaction (manual + automatic):**
- `/compact` calls `runner.CompactHistories`, which runs each adapter
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

## Package Import Graph

```
tools          ← stdlib only
pricing        ← stdlib only
mcp            ← tools (for ToolDef, MCPDispatcher)
models         ← tools (for FileWrite, tool names, ExecuteRead/ApplyWrites)
config         ← stdlib only
commands       ← stdlib only
prompthistory  ← stdlib only
checkpoint     ← models, tools (for FileWrite conversion)
adapters       ← models, pricing, tools, config, provider SDKs
runner         ← models, pricing, checkpoint
diff           ← os, strings, sergi/go-diff
history        ← models, encoding/json, os
logging        ← models (ModelAdapter, ModelResponse), stdlib
preferences    ← models (for ModelResponse latency/ID), encoding/json, os
ui             ← models, pricing, tools, runner, diff, history, adapters, config, commands, prompthistory, checkpoint, bubbletea, lipgloss
cmd/errata     ← config, adapters, pricing, logging, ui, mcp, tools
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
            // All tool dispatch goes through adapters.DispatchTool():
            list_directory → ExecuteListDirectory(), emit AgentEvent{Type:"reading"}
            search_files   → ExecuteSearchFiles(),   emit AgentEvent{Type:"reading"}
            search_code    → ExecuteSearchCode(),    emit AgentEvent{Type:"reading"}
            read_file      → ExecuteRead(),          emit AgentEvent{Type:"reading"}
            bash           → ExecuteBash(),          emit AgentEvent{Type:"bash"}
            write_file     → append to proposed[],  emit AgentEvent{Type:"writing"}, ack "queued"
    }
    if no tool calls → break
    append tool results to messages
    EmitSnapshot(onEvent, qualifiedID, textParts, start, totalInput, totalOutput, proposed)
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

**Turn-boundary snapshots:** `EmitSnapshot` (in `common.go`) serialises a `models.PartialSnapshot`
to JSON and emits it as `AgentEvent{Type: "snapshot"}` at the end of every loop iteration.
The runner intercepts these events for incremental checkpointing and never forwards them to
the UI. The snapshot is not emitted on the final turn (no tool calls → `break` before reaching
the end of the loop body); `MarkCompleted` handles final state instead.

**Tool dispatch is centralised:** All adapters call `adapters.DispatchTool(ctx, name, args, onEvent, &proposed)`
from `internal/adapters/common.go`. `DispatchTool` first checks MCP dispatchers in context, then
falls through to the built-in switch. Adding a built-in tool requires only adding a `ToolDef` to
`tools.Definitions` in `internal/tools/tools.go` and a new case in `DispatchTool`.

**Tool availability is context-scoped:** The active tool set (after `/tools off` filtering) is stored
in the request `context.Context` via `tools.WithActiveTools`. Each adapter reads it with
`tools.ActiveToolsFromContext(ctx)` to build the tool list passed to the API.

**MCP dispatchers are context-scoped:** MCP tool dispatch functions are stored in context via
`tools.WithMCPDispatchers`. `DispatchTool` reads them and calls the matching dispatcher before
any built-in case, so MCP tools can shadow or extend the built-in set. The TUI (`launchRun`)
builds the combined active-tool context before passing it to `runner.RunAll`.

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
          ↓          ↓
       (cancel)   (skip / no writes)
```

- **idle**: `textarea` visible, slash commands handled, history shown
- **running**: agent panels rendered live; goroutines send `agentEventMsg` via `prog.Send()`;
  ESC or Ctrl-C cancels the run and returns to idle with partial results preserved
- **selecting**: diff view + numbered menu; key input collected character by character
- **cancel shortcut**: if user presses ESC/Ctrl-C during running, `cancelRun()` fires, checkpoint
  is saved, and the TUI returns to idle with "Interrupted (models). /resume to continue."
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
Used by the TUI (`internal/ui/diff.go`).

---

## Model Configuration

```bash
# .env — API keys and credentials only
# All behavioural config (models, system prompt, MCP servers, constraints, etc.)
# is configured via recipe.md. See recipe.example.md for the full reference.
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...

# OpenRouter — access any model via a single API key
OPENROUTER_API_KEY=sk-or-...

# LiteLLM — self-hosted proxy; base URL must include /v1
LITELLM_BASE_URL=http://localhost:4000/v1
LITELLM_API_KEY=optional
```

Debug logging is enabled via the `--debug-log` CLI flag:
```bash
./errata --debug-log data/log.jsonl
```

Default models (auto-detected from available API keys; native providers only):

| Provider  | Default model        | ID routing rule              |
|-----------|----------------------|------------------------------|
| Anthropic | `claude-sonnet-4-6`  | prefix `claude`              |
| OpenAI    | `gpt-4o`             | prefix `gpt-`, `o1`, `o3`    |
| Google    | `gemini-2.0-flash`   | prefix `gemini`              |
| OpenRouter | _(none; must set recipe ## Models)_ | contains `/` |
| LiteLLM   | _(none; must set recipe ## Models)_ | prefix `litellm/` |

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

## MCP Tool Servers

Errata supports the [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) for
extending the tool set at runtime. Any MCP server that speaks stdio transport and exposes
the `tools` capability can be connected via the recipe `## MCP Servers` section.

### Configuration format (recipe.md)

```markdown
## MCP Servers
- name1: command arg1 arg2
- name2: command
```

- Bullet list of `name: command` pairs
- `name` is used in log messages only
- `command` is passed to `exec.Command` (the subprocess inherits the full process environment)
- API keys for the MCP server (e.g. `EXA_API_KEY`) should be set in `.env` alongside Errata's own keys

### Startup behavior

`mcp.StartServers` is called in `setupAdapters` (non-fatal):
- Each server subprocess is launched, MCP handshake completed, and `tools/list` called
- Tool definitions are merged into the active tool set before the first run
- A server that fails to start or handshake is logged as a warning and skipped
- All subprocesses are killed on clean exit via the `cleanup` deferred function

### MCP tool dispatch flow

1. `launchRun` builds `activeDefs` by combining `tools.ActiveDefinitions(disabled)` + `tools.FilterDefs(mcpDefs, disabled)` — both respect `/tools off`
2. `tools.WithActiveTools(ctx, activeDefs)` stores the combined list
3. `tools.WithMCPDispatchers(ctx, dispatchers)` stores the call functions
4. Each adapter reads `ActiveToolsFromContext(ctx)` to build the tool list sent to the API
5. When the model calls an MCP tool, `DispatchTool` finds it in `MCPDispatchersFromContext(ctx)` and calls the dispatcher, which calls `conn.CallTool` on the subprocess

### Known MCP servers

| Provider | Package | Required env var | Example tools |
|----------|---------|-----------------|---------------|
| [Exa](https://exa.ai) | `npx @exa-ai/exa-mcp-server` | `EXA_API_KEY` | `search`, `find_similar`, `get_contents` |
| [Brave Search](https://brave.com/search/api/) | `npx @modelcontextprotocol/server-brave-search` | `BRAVE_API_KEY` | `brave_web_search`, `brave_local_search` |
| [Tavily](https://tavily.com) | `npx @tavily-mcp/server` | `TAVILY_API_KEY` | `tavily_search` |
| Filesystem | `npx @modelcontextprotocol/server-filesystem /path` | — | `read_file`, `list_directory`, `write_file` |
| GitHub | `npx @modelcontextprotocol/server-github` | `GITHUB_TOKEN` | `create_issue`, `list_prs`, `get_file_contents` |

### Tool management with MCP

MCP tools appear alongside built-in tools in `/tools` output (labeled `(mcp)`):

```
  [on ] read_file
  [on ] bash
  [on ] search        (mcp)   ← Exa search tool
  [off] find_similar  (mcp)   ← disabled for this session
```

`/tools off search` and `/tools on search` work identically for MCP tool names.

### Schema translation

MCP `inputSchema` (JSON Schema) is translated to Errata's `ToolDef` properties on connection:
- `properties` → `map[string]ToolParam{Type, Description}`
- `required` → `[]string`
- Only `string` and `integer` parameter types are used (all others become `string`)
- Nested schemas are flattened — only top-level properties are exposed to the model

---

## Deployment Configuration

Errata is designed to be used as a development harness that matches your real-world agentic
setup. Key configuration knobs for production/team deployments:

### Custom system prompt (recipe `## System Prompt`)

Injected after the built-in tool guidance in every adapter's system prompt:

```markdown
## System Prompt
You are working on the Acme platform, a Go monorepo at /opt/acme.
The main service is in cmd/acme/. Always run `go test ./...` before proposing writes.
The team uses conventional commits (feat:, fix:, chore:).
```

Implementation: `tools.SetSystemPromptExtra(cfg.SystemPromptExtra)` is called once at
startup in `setupAdapters`. `SystemPromptSuffix()` appends the extra text to its return
value, which all five adapters call when constructing the system message.

### Restricting the tool set

Disable tools globally for a deployment by starting with `/tools off <name>` as the
first command, or by building a wrapper script:

```bash
# Kiosk mode: code search only, no shell or web access
./errata <<< '/tools off bash web_fetch web_search'
```

For persistent per-project defaults, the `/tools off` state is saved to `.errata_tools`
(cwd-local) so it survives session restarts.

### Pointing at a self-hosted model proxy

Set `LITELLM_BASE_URL` in `.env` and specify models via recipe:

```markdown
## Models
- litellm/llama-3-70b
- litellm/codestral
```

### Restricting to specific models

```markdown
## Models
- claude-opus-4-6
- gpt-4o
```

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

## Testing & Linting

```bash
go test ./...                  # all packages
go test -v ./internal/runner   # single package, verbose
go test -run TestExecuteRead   # single test
golangci-lint run ./...        # run all configured linters
```

Test files: `*_test.go` alongside source in the same directory.
Stub adapters implement `ModelAdapter` in test files — no shared fixture infrastructure.
Table-driven tests preferred for config, preferences, and diff packages.

**Testing requirements for every change:**
- Any new function or package must have accompanying tests.
- Any bug fix must include a regression test that would have caught the original bug.
- Any struct that is serialized to disk (JSON, JSONL) must have a round-trip test
  (write → read → assert values) to catch unexported-field and missing-json-tag bugs.
- Run `go test ./...` and `golangci-lint run ./...` before considering a task complete.
- For diff/transformation output, use flexible assertions (check for key substrings or
  structural properties) rather than exact string matching — output formats may vary
  depending on the Myers diff algorithm's choice of common subsequence.

**Linting rules (`.golangci.yml`):**
- 16 linters enabled: standard defaults (errcheck, govet, ineffassign, staticcheck, unused)
  plus bodyclose, errorlint, forcetypeassert, gocritic, gosec, musttag, nilerr, nilnil,
  modernize, prealloc, testifylint.
- `func (a App)` value-receiver methods in `internal/ui/` require `//nolint:gocritic` because
  bubbletea's `tea.Model` interface mandates value receivers.
- Use `require.NoError`/`require.Error` (not `assert`) for error checks that guard subsequent
  assertions. Use `assert.Empty` instead of `assert.Equal(t, "", ...)`.
- Directory permissions should be `0o750`, file permissions `0o600` (gosec G301/G306).
- Prefer `strings.SplitSeq` over `strings.Split` in range loops (modernize).

---

## Files to Never Commit

- `.env`
- `data/preferences.jsonl` (contains prompt history)
- `data/log.jsonl` (contains full prompt + response content)
- `data/history.json` (contains full conversation context)
- `data/prompt_history.jsonl` (contains submitted prompts for Up-arrow / Ctrl-R recall)
- `data/checkpoint.json` (transient interrupted run state for `/resume`)
- `dist/` (compiled binaries from `make build-all`)
