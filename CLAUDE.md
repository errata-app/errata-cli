# Errata — A/B Testing Tool for Agentic AI Models

## Project Overview

**Errata** is a CLI tool that sends programming prompts to multiple AI models simultaneously,
runs each as a coding agent with `read_file` / `write_file` tools, shows live tool-event panels,
lets the user select the best proposal, and applies the winner's file writes to disk.
Every selection is logged for preference analysis over time.

---

## Stack

- **Language:** Go 1.23+
- **CLI:** `github.com/spf13/cobra` — subcommand routing and `--help`
- **TUI:** `github.com/charmbracelet/bubbletea` + `github.com/charmbracelet/lipgloss`
- **AI SDKs:** `anthropic-sdk-go v1.26`, `openai-go v1.12`, `google/generative-ai-go v0.20.1`
- **Config:** `github.com/joho/godotenv` + `os.Getenv`
- **Preferences:** append-only JSONL at `data/preferences.jsonl`
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
│   │   ├── anthropic.go     # AnthropicAdapter.RunAgent()
│   │   ├── openai.go        # OpenAIAdapter.RunAgent()
│   │   └── gemini.go        # GeminiAdapter.RunAgent()
│   ├── runner/
│   │   └── runner.go        # RunAll() — goroutines + sync.WaitGroup
│   ├── tools/
│   │   └── tools.go         # FileWrite, ToolDef, ExecuteRead(), ApplyWrites()
│   ├── diff/
│   │   └── diff.go          # Compute() → FileDiff (LCS; shared by TUI + future web)
│   ├── preferences/
│   │   └── preferences.go   # Record(), LoadAll(), Summarize()
│   └── ui/
│       ├── app.go           # bubbletea program, mode state machine
│       ├── panels.go        # live agent panel rendering
│       ├── diff.go          # diff + selection menu rendering
│       └── keys.go          # key bindings
├── go.mod / go.sum
└── Makefile
```

---

## Key Commands

```bash
go build -o errata ./cmd/errata   # build binary
./errata                           # start TUI REPL
./errata stats                     # print preference summary (non-interactive)
make test                          # go test ./...
make lint                          # golangci-lint run ./...
make build-all                     # cross-compile darwin/linux/windows to dist/
```

---

## REPL Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/verbose` | Toggle verbose mode (model text alongside tool events) |
| `/models` | List currently active models |
| `/exit` or `/quit` | Exit |
| `Ctrl-D` | Exit |

---

## Core Workflow

1. User types a prompt in the REPL
2. `runner.RunAll()` fans out to all adapters concurrently via goroutines
3. Each adapter runs a multi-turn agentic loop:
   - `read_file` calls execute immediately (path-traversal guarded, read-only)
   - `write_file` calls are **intercepted** — stored as proposals, not written to disk
   - Loop exits when the model stops calling tools
4. `ui` renders live tool-event panels while goroutines run
5. After all agents finish, a compact diff view shows each model's proposed changes
6. User selects a number; that model's `ProposedWrites` are applied via `tools.ApplyWrites()`
7. Preference entry appended to `data/preferences.jsonl`

Agent timeout: **5 minutes** per adapter (`context.WithTimeout`).

---

## Package Import Graph

Understanding the import relationships prevents cycles:

```
tools       ← stdlib only
models      ← tools (for FileWrite, tool names, ExecuteRead/ApplyWrites)
config      ← stdlib only
runner      ← models, context, sync
diff        ← os, strings, sergi/go-diff
preferences ← models (for ModelResponse latency/ID), encoding/json, os
ui          ← models, tools, runner, diff, bubbletea, lipgloss
cmd/errata  ← config, models, preferences, ui
```

**Critical:** `tools.FileWrite` lives in `internal/tools`, not `internal/models`.
This is intentional — moving it to `models` would create a cycle since adapters
(inside `models`) import `tools`, and `tools.ApplyWrites` needs `FileWrite`.

---

## Agentic Tool Loop Pattern

Each adapter (`anthropic.go`, `openai.go`, `gemini.go`) follows the same pattern:

```go
for {
    resp := callAPI(messages, tools)
    for _, block := range resp.Content {
        if block is text  → collect text, optionally emit AgentEvent{Type:"text"}
        if block is tool_use:
            read_file  → ExecuteRead(), emit AgentEvent{Type:"reading"}, feed result back
            write_file → append to proposed[], emit AgentEvent{Type:"writing"}, ack "queued"
    }
    if no tool calls → break
    append tool results to messages, loop
}
return ModelResponse{..., ProposedWrites: proposed}
```

The loop always feeds tool results back before the next turn. Writes are **never** executed
inside the loop — they accumulate in `proposed []tools.FileWrite` and are returned in
`ModelResponse.ProposedWrites`.

---

## Provider SDK Notes

Each SDK has its own naming conventions for the same concepts. Key details:

### Anthropic (`anthropic-sdk-go v1.26`)
- Response content: `[]ContentBlockUnion` — use `.AsText()`, `.AsToolUse()` to downcast
- Tool input is `json.RawMessage` on `ToolUseBlock.Input` — unmarshal manually
- Multi-turn: call `resp.ToParam()` to convert a response message back to a `MessageParam`
- Tool results: `anthropic.NewToolResultBlock(toolUseID, content, isError)` → `ContentBlockParamUnion`
- Tool definitions: `ToolUnionParam{OfTool: &ToolParam{...}}` with `ToolInputSchemaParam`
- `anthropic.String(s)` wraps a string in `param.Opt[string]` for optional fields

### OpenAI (`openai-go v1.12`)
- Convenience constructors: `openai.UserMessage(s)`, `openai.ToolMessage(content, toolCallID)`
- Multi-turn: `msg.ToParam()` converts a `ChatCompletionMessage` back to `ChatCompletionMessageParamUnion`
- Tool calls in response: `choice.Message.ToolCalls []ChatCompletionMessageToolCall`
- Function arguments are a JSON string: `json.Unmarshal([]byte(tc.Function.Arguments), &input)`
- Tool definitions: `ChatCompletionToolParam{Function: shared.FunctionDefinitionParam{...}}`
- `shared.FunctionParameters` is `map[string]any` — pass the full JSON schema object

### Gemini (`google/generative-ai-go v0.20.1`)
- Use `genai.NewClient(ctx, option.WithAPIKey(key))` then `client.GenerativeModel(modelID)`
- Tool-use loop via `model.StartChat()` → `chat.SendMessage(ctx, parts...)`
- Response parts: `resp.Candidates[0].Content.Parts` — type-switch on `genai.Text` vs `genai.FunctionCall`
- Tool results: send back `genai.FunctionResponse{Name, Response}` as the next message parts
- Tool schemas: `*genai.Tool{FunctionDeclarations: []*genai.FunctionDeclaration{...}}`
  with `*genai.Schema{Type: genai.TypeObject, Properties: ..., Required: ...}`
- `extractStringArgs` helper converts `map[string]any` args to `map[string]string`

---

## TUI Architecture (bubbletea)

The TUI uses the Elm architecture: `Model → View`, `Update` on messages.

### Modes (state machine)
```
idle → running → selecting → idle
                    ↓
                 (skip)
```

- **idle**: `textarea` visible, slash commands handled, history shown
- **running**: agent panels rendered live; goroutines send `agentEventMsg` via `prog.Send()`
- **selecting**: diff view + numbered menu; key input collected character by character

### Goroutine → TUI communication
```go
go func() {
    responses := runner.RunAll(ctx, adapters, prompt,
        func(modelID string, event models.AgentEvent) {
            prog.Send(agentEventMsg{modelID, event})  // safe from any goroutine
        }, verbose)
    prog.Send(runCompleteMsg{responses})
}()
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

- All rendering (lipgloss, panel layout, diff colors) lives in `internal/ui/` — no lipgloss imports elsewhere
- Tool schemas are defined once in `internal/tools/` and translated per-provider in each adapter
- `internal/diff/` has no external dependencies — keep it that way for future web reuse
- Preferences are append-only — always `O_APPEND|O_CREATE`, never truncate
- If a model's API key is missing, skip it with a warning; never crash on missing keys
- Context cancellation (`ctx.Done()`) propagates through all adapter API calls automatically
- Each adapter has a compile-time interface check: `var _ ModelAdapter = (*XAdapter)(nil)`

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
- `dist/` (compiled binaries from `make build-all`)
