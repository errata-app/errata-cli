# Errata — Development Scratchpad

---

## 2026-02-23 — TUI Chat-Style Persistent Feed

### Motivation
The TUI collapsed live panel boxes into flat `history []string` strings on run completion.
Failed model responses were silently omitted. No scrollable history of past prompts.

### Changes

**`internal/ui/panels.go`**
- Added `errMsg string` field to `panelState`
- `renderPanel`: errored panels get red border + `"error: <msg>"` status line

**`internal/ui/diff.go`**
- `RenderDiffs`: failed responses now shown with a red error header + message (previously skipped)
- `RenderSelectionMenu`: errors listed as non-selectable `"-"` lines; only OK responses are numbered (selIdx counter vs old i+1)

**`internal/ui/app.go`** — major refactor
- New `feedItem` type (`kind: "message"|"run"`, `panels []*panelState`, `responses`, `note`)
- Replaced `history []string` + separate diff `vp` with `feed []feedItem` + single `feedVP viewport.Model`
- `withFeedRebuilt(gotoBottom bool) App` and `withMessage(text string) App` — value-returning helpers that fit bubbletea's functional update pattern
- Run entries pushed to feed immediately on prompt submit; `[]*panelState` pointers shared between `a.panels` and the feed item so live updates propagate automatically
- `runCompleteMsg` uses **index-based** panel matching (`results[i] == panels[i]`) — fixes latent bug where resolved model IDs (e.g. `claude-sonnet-4-6-20250220`) couldn't find the panel keyed by the configured ID
- ↑/↓/pgup/pgdn scrolls the feed in idle and selecting modes
- `WindowSizeMsg` preserves scroll position (`AtBottom()` check before resize)
- `selectionErr string` field for inline selection error without polluting the feed
- Diff rendered inline in the feed below frozen panels (no separate viewport needed)

**`internal/ui/diff_test.go`**
- Updated `TestRenderDiffs_SkipsFailedResponses` → `TestRenderDiffs_ShowsFailedResponses`
- Updated `TestRenderSelectionMenu_SkipsFailedResponses` → `TestRenderSelectionMenu_ShowsFailedResponsesAsNonSelectable`

### Result
`go build ./...` clean, `go test ./...` all green.

---

## 2026-02-23 — Verbose-gate error events in panel boxes

### Motivation
In non-verbose mode, error events (the inline streaming "error    …" line inside a panel box)
were always shown, inconsistent with text events which are suppressed when verbose is off.

### Changes

**`internal/runner/runner.go`**
- Wrapped the `onEvent(AgentEvent{Type:"error",...})` call with `if verbose` — mirrors how
  adapters gate "text" events.

**`internal/runner/runner_test.go`**
- `TestRunAll_ErrorSurfacesViaOnEvent` split into two tests:
  - `TestRunAll_ErrorSurfacesViaOnEventVerbose` — verbose=true, error event IS emitted
  - `TestRunAll_ErrorEventSuppressedNonVerbose` — verbose=false, no events emitted

### Result
`go build ./...` clean, `go test ./...` all green.
Panel status line ("error: …" in red) still always shows — only the inline event list
entry is gated on verbose.

---

## 2026-02-23 — Refactor verbose filtering out of adapters

### Motivation
Each of the 5 adapter files duplicated the same `if verbose { onEvent(...) }` guard.
Verbose is a UI/caller concern and shouldn't be baked into the adapter interface.

### Changes

**`internal/models/base.go`**
- Removed `verbose bool` from the `ModelAdapter.RunAgent` interface signature.

**`internal/models/{anthropic,openai,gemini,openrouter,litellm}.go`**
- Removed `verbose bool` parameter from each `RunAgent` implementation.
- Adapters now always call `onEvent(AgentEvent{Type: "text", ...})` unconditionally.

**`internal/logging/logger.go`**
- Removed `verbose bool` from `loggingAdapter.RunAgent` signature and inner call.

**`internal/runner/runner.go`**
- Added local `filtered` closure that suppresses "text" and "error" events when `!verbose`.
- Passed `filtered` to `RunAgent` instead of raw `onEvent`.
- Runner error event now also routed through `filtered` (consolidates both verbose gates).

**`internal/models/base_test.go`, `internal/runner/runner_test.go`**
- Updated stub `RunAgent` signatures to match new interface.

### Result
`go build ./...` clean, `go test ./...` all green.

---

## 2026-02-23 — Fix pricing cache (unexported fields + zero-value guard)

### Motivation
`modelPricing` used unexported fields (`inputPMT`, `outputPMT`). `encoding/json`
silently ignores unexported fields, so the cache was written as `{}` for every entry.
On next startup the cache appeared valid (`len > 0`) but all prices deserialized as 0,
causing `CostUSD` to always return 0 for all models including `gpt-4o-2024-08-06`.

### Root cause
```go
// before: prices serialized as {} — lost on disk
type modelPricing struct{ inputPMT, outputPMT float64 }
```

### Changes

**`internal/models/pricing.go`**
- Exported fields: `InputPMT float64 \`json:"input_pmt"\`` / `OutputPMT float64 \`json:"output_pmt"\``
- Updated all internal usages (`p.inputPMT` → `p.InputPMT`, struct literals → named fields)
- Zero-value guard in `readPricingCache`: if all entries have `{0, 0}` prices, return `nil`
  so a fresh OpenRouter fetch is triggered instead of using corrupt cached data
- Deleted `data/pricing_cache.json` (corrupt) — next startup fetches correct prices

### Result
`go build ./...` clean, `go test ./...` all green.

---

## 2026-02-23 — Adapter shared helpers (common.go)

### Motivation
All 5 adapters duplicated `extractStringMap`, `join`, and the tool-dispatch/response-building
pattern. Refactored into shared helpers so each adapter only handles SDK-specific API calls.

### Changes

**`internal/adapters/common.go`** (new)
- `extractStringMap(map[string]any) map[string]string` — converts Gemini `Args` to string map
- `join([]string) string` — joins text parts
- `DispatchTool(name, args, onEvent, *proposed) (result string, ok bool)` — single dispatch point
- `BuildErrorResponse(modelID, qualifiedID, err, start) (ModelResponse, error)`
- `BuildSuccessResponse(modelID, qualifiedID, texts, start, in, out, proposed) (ModelResponse, error)`

**`internal/adapters/{anthropic,openai,gemini,openrouter,litellm}.go`**
- Removed duplicated `extractStringMap`, `join` bodies
- Replaced inline tool dispatch + response construction with the common helpers
- Removed `pricing` imports from all except `gemini.go`

**`internal/adapters/common_test.go`** (new)
- `TestDispatchTool_ReadEmitsEventAndReturnsContent` — uses `t.Chdir(dir)` + relative path
  to satisfy `tools.ExecuteRead`'s path-boundary guard (temp dirs are outside cwd)
- `TestDispatchTool_WriteDoesNotExecute`

### Result
`go build ./...` clean, `go test ./...` all green.

---

## 2026-02-23 — Context window % in web UI

### Motivation
TUI panels showed `~N% ctx` but the web UI never displayed context fill at all.

### Changes

**`internal/web/handlers.go`**
- Added `ContextWindowTokens int64 \`json:"context_window_tokens"\`` to `responseData`
- `buildCompletePayload` calls `pricing.ContextWindowTokens(resp.ModelID)` per response

**`internal/web/static/app.js`**
- New `fmtStat(resp)` helper unifies all stat formatting across the three display sites
  (live panel header, history panel, diff/selection header)
- Shows `~N% ctx` when `context_window_tokens > 0 && input_tokens > 0`
- Uses `input_tokens / context_window_tokens * 100` — accurate for all messages including
  the first (where no prior history exists yet)
- `slimResponse` includes `context_window_tokens`

### Result
`go build ./...` clean, `go test ./...` all green. ctx% now shows on every response including
the first message in a session.

---

## 2026-02-23 — Persistent conversation history

### Motivation
Closing the browser tab or TUI process destroyed the per-model conversation history.
Users lost context on every restart. History should survive client restarts; only an
explicit `/clear` should wipe it.

### New package: `internal/history/history.go`

```go
func Load(path string) (map[string][]models.ConversationTurn, error)  // nil on missing file
func Save(path string, h map[string][]models.ConversationTurn) error   // atomic: .tmp → Rename
func Clear(path string) error                                           // os.Remove; ignore IsNotExist
```

Storage: `data/history.json` — `map[string][]ConversationTurn` keyed by model ID.
Unknown model IDs silently ignored. Corrupt JSON → warn + start fresh.

### Web: server-level histories

Moved `histories` from a per-connection local var in `handleWS` to a field on `Server`
guarded by `sync.RWMutex`. All browser tabs share one history; reconnecting picks up
where the previous connection left off.

**`internal/web/server.go`**
- Added `histPath string`, `histMu sync.RWMutex`, `histories map[string][]ConversationTurn`
- `New(adapters, prefPath, histPath)` loads history from disk at construction

**`internal/web/handlers.go`**
- All history reads use `s.histMu.RLock()`; all writes use `s.histMu.Lock()`
- `history.Save` called after each run completion and after `/compact`
- New `clear_history` WebSocket message: sets `s.histories = nil`, calls `history.Clear`, sends `history_cleared`

**`internal/web/static/app.js`**
- `/clear` handler now sends `{ type: 'clear_history' }` to server
- Handles `history_cleared` confirmation message

### TUI: load on start, save after every mutation

**`internal/ui/app.go`**
- Added `histPath string` field to `App`
- `New()`/`Run()` take `histPath string`; load at startup
- `history.Save` called after run completion and after compact
- `history.Clear` called on `/clear`

### Config + wiring

**`internal/config/config.go`**: `HistoryPath: "data/history.json"` default
**`cmd/errata/main.go`**: passes `cfg.HistoryPath` to both `ui.Run` and `web.New`

### Result
`go build ./...` clean, `go test ./...` all green.
