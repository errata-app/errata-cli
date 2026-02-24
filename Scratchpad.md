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

---

## 2026-02-24 — Provider-tagged model routing + set_models protocol upgrade

### Motivation
Novel model IDs that don't match a known prefix (e.g. a newly released `ricky` from OpenAI)
appeared in the web models panel but could not be activated — `NewAdapter` returned
`"unknown model prefix for 'ricky'"`. The panel already knows which provider each model
belongs to; that hint needed to reach the server.

### Changes

**`internal/adapters/registry.go`**
- Added `NewAdapterForProvider(modelID, provider string, cfg) (ModelAdapter, error)` —
  dispatches directly on the human-readable provider name ("Anthropic", "OpenAI", "Gemini"),
  bypassing prefix guessing. Falls back to `NewAdapter` for OpenRouter/LiteLLM/unknown.

**`internal/web/handlers.go`**
- New `ModelSpec{ID, Provider string}` struct (JSON: `{id, provider}`)
- `wsClientMsg.ModelSpecs []ModelSpec` replaces `ModelIDs []string` for `set_models`
- `set_models` handler calls `adapters.NewAdapterForProvider(spec.ID, spec.Provider, s.cfg)`
  when the model isn't already in `s.adapters`

**`internal/web/static/app.js`**
- `renderModelsPanel`: `cb.dataset.provider = p.name` stored on each checkbox
- `applyModels`: collects `{id, provider}` objects instead of bare IDs

**`internal/adapters/registry_test.go`** — `TestNewAdapterForProvider_*` (4 cases)
**`internal/web/handlers_test.go`** — `TestHandleWS_SetModels_*` (4 cases)

### Result
`go build ./...` clean, `go test ./...` all green.

---

## 2026-02-24 — Stats enrichment: per-model session cost in `/stats` and web Stats button

### Motivation
The web Stats button showed only preference win counts from the preference JSONL.
Session cost was only accessible via the separate `/totalcost` command.
The TUI had no `/stats` slash command at all.

### Changes

**`internal/web/static/app.js`**
- Added `let sessionCostPerModel = {}` state variable
- `complete` handler now accumulates per-model cost alongside the existing `sessionCostUSD` total
- Rewrote `showStats()`: two sections — "Preference wins" (sorted by count desc) and
  "Session cost" (sorted by cost desc, with "Total" line; section omitted when all zero)
- Added `/stats` slash command guard in `handleSend()` (was only wired to the Stats button)

**`internal/ui/app.go`**
- Added `sessionCostPerModel map[string]float64` to `App` struct; initialized in `New()`
- `runCompleteMsg` accumulates `a.sessionCostPerModel[resp.ModelID] += resp.CostUSD`
- Added `/stats` case: same two-section layout as web
- Updated `helpText()` to document `/stats`

### Result
`go build ./...` clean, `go test ./...` all green.

---

## 2026-02-24 — Dynamic slash command suggestions

### Motivation
Users had to already know the available slash commands. Both surfaces should show a live
filtered list of matching commands whenever the input starts with `/`, updating on every
keystroke — matching the behaviour of Claude Code's slash command UI.

### Changes

**`internal/ui/app.go`**
- Added `var slashCommands []struct{name, desc string}` with all 9 TUI commands
- `View()` in `modeIdle`: after the textarea, renders matching commands (name in teal,
  description in dim grey) whenever `a.input.Value()` starts with `/`; filtered live
- `handleIdleKey()`: intercepts `tea.KeyTab` — completes the first matching command via
  `a.input.SetValue(c.name + " ")` + `a.input.CursorEnd()`

**`internal/web/static/index.html`**
- Restructured `#input-area` footer: flex-column with `<div id="slash-completions">` above
  a new `.input-row` wrapper (textarea + send button)

**`internal/web/static/style.css`**
- `#input-area` → `flex-direction: column; padding: 0 18px 12px`
- New `.input-row` mirrors old row layout
- New `#slash-completions` (hidden by default, `.visible` class to show),
  `.slash-item`, `.slash-item-name`, `.slash-item-desc` styles

**`internal/web/static/app.js`**
- Added `const SLASH_COMMANDS` array (6 web commands + descriptions)
- Added `let activeSlashIdx = -1` state
- Added `updateSlashCompletions()` — filters, renders rows, wires mousedown handlers
- Added `hideSlashCompletions()` — clears and hides the panel
- `inputEl.addEventListener('input', updateSlashCompletions)` — fires on every keystroke
- `keydown` handler extended: ArrowUp/Down navigate list, Tab completes first/active match,
  Enter completes when an item is active, Escape dismisses
- `handleSend()` calls `hideSlashCompletions()` at top

### Result
`go build ./...` clean, `go test ./...` all green.

---

## 2026-02-24 — Canonical slash command registry (`internal/commands`)

### Motivation
Slash command metadata (name + description) was duplicated: `var slashCommands` in
`internal/ui/app.go` and `const SLASH_COMMANDS` in `app.js`. Descriptions had drifted.
Web was missing `/verbose` and `/help`. Any future addition required three separate edits.

### New package: `internal/commands/commands.go`

```go
type Command struct { Name, Desc string; TUIOnly bool }
var All = []Command{...}   // 9 commands, /exit marked TUIOnly
func Web() []Command        // filters out TUIOnly entries
```

Zero imports — leaf package safe to import from both `ui` and `web`.

### Changes

**`internal/ui/app.go`**
- Removed `var slashCommands` (local duplicate)
- `View()` modeIdle suggestion rendering → `commands.All`
- `handleIdleKey()` Tab completion → `commands.All`
- `helpText()` now generates from `commands.All`

**`internal/web/handlers.go`** — new `handleCommands`: serves `commands.Web()` as JSON

**`internal/web/server.go`** — registered `GET /api/commands`

**`internal/web/static/app.js`**
- Removed hardcoded `const SLASH_COMMANDS`; replaced with `let slashCommands = []`
- `loadCommands()` fetches `/api/commands` at init
- Added `/help` and `/verbose` handlers in `handleSend()`

### Result
`go build ./...` clean, `go test ./...` all green.
Web gains `/help` and `/verbose`. All descriptions now come from a single source.

---

## 2026-02-24 — Full DRY & code smell remediation

### Motivation
A full audit of all packages (core engine, UI/web Go, support packages, frontend) surfaced two
real bugs, several dead-code / stdlib-reimplementation issues, meaningful duplication across the
two UI surfaces, and some oversized functions.

### Batch 0 — Real Bugs

**B0-1 (`internal/web/static/app.js`):** `/model` slash command was sending bare string IDs
(`model_ids: ids`) instead of `ModelSpec` objects. Go's `json.Unmarshal` silently zero-valued
the mismatched type, so the filter was never applied. Fixed:
```js
model_ids: ids.map(id => ({ id, provider: '' }))
```
Matches the `onopen` reconnect path that was already correct.

**B0-2 (`internal/pricing/pricing.go`):** `ContextWindowTokens` used `strings.LastIndex` for
the qualified→bare fallback while `CostUSD` and `PricingFor` used `strings.Index`. Fixed as a
side-effect of extracting `resolvePricing`.

### Batch 1 — Quick Hygiene

- **`internal/runner/runner.go`**: Removed stale `// Silly little comment to test prs`
- **`internal/ui/keys.go`**: Deleted — `keyMap`/`keys` were defined but never referenced
- **`internal/logging/logger.go`**: Replaced hand-rolled `dirOf()` with `filepath.Dir()`; added `path/filepath` import
- **`internal/preferences/preferences.go`**: Replaced hand-rolled `splitLines()` with `strings.Split(…, "\n")`; added `"strings"` import

### Batch 2 — Shared Utility Extractions

**B2-1 — `pricing.ProviderQualifiedID`:** Identical `providerQualifiedID()` function existed in
both `ui/app.go` and `web/handlers.go`. Exported from `internal/pricing`; private copies deleted.

**B2-2 — `resolvePricing`:** `CostUSD`, `PricingFor`, and `ContextWindowTokens` all duplicated
the qualified→bare ID fallback. Extracted into a private `resolvePricing(qualifiedID)` helper in
`pricing.go`. Also fixes B0-2.

**B2-3 — `logging.RandomHex`:** `newSessionID()` (16 bytes, `cmd/errata/main.go`) and
`newRunID()` (8 bytes, `logger.go`) both generated random hex. Exported `RandomHex(n int)` from
`logging`; replaced both private functions.

**B2-4 — `setupAdapters`:** `runREPL` and `runServe` shared 90% of setup code. Extracted into
`setupAdapters(cfg) (ads, sessionID, warnings, cleanup)` in `main.go`.

### Batch 3 — Function Decomposition

**B3-1 — `handleWS` (284 lines → ~30-line dispatch):** Introduced `wsConn` per-connection struct
with 6 extracted methods: `wsHandleRun`, `wsHandleSelect`, `wsHandleSetModels`, `wsHandleCancel`,
`wsHandleCompact`, `wsHandleClearHistory`.

**B3-2 — `handlePrompt` (176 lines → ~20-line dispatch):** Extracted per-command methods
following the existing `handleModelCommand` pattern: `handleVerboseCmd`, `handleModelsListCmd`,
`handleClearCmd`, `handleCompactCmd`, `handleStatsCmd`, `launchRun`.

### Batch 4 — Frontend Cleanup

**B4-1 — `handleLocalCommand(text)`:** All 3 local-command handlers in `app.js` repeated the
same 4-line boilerplate (clear input, push to history, saveHistory, appendHistoryMsg). Extracted
to a helper; saves ~12 lines.

**B4-2 — Magic-number constants:** Added `HISTORY_DISPLAY_CAP = 50`, `PROMPT_PREVIEW_LEN = 90`,
`ERROR_TRUNCATE_LEN = 100` near the top of `app.js`; replaced all 6 inline literals.

### Batch 5 — Surface Parity

**B5-1:** Web's `buildCompletePayload` now applies the same context-overflow hint that the TUI
shows: when `runner.IsContextOverflowError(resp.Error)` is true, appends
`" — use /clear or /compact to reset"` to the error text.

### Result
`go build ./...` clean, `go test ./...` all green.
