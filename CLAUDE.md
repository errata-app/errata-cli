# Errata — A/B Testing Tool for Agentic AI Models

Errata sends programming prompts to multiple AI models simultaneously, runs each as a
full coding agent, shows live tool-event panels, lets the user select the best proposal,
and applies the winner's file writes to disk.

This is a Go-primary project. Default to Go idioms, conventions, and tooling.

---

## Build & Test Workflow

After implementing changes, always run `go build ./...`, `go vet ./...`, `go test ./...`,
and `golangci-lint run ./...` before committing. Fix any issues before proceeding.

---

## Git Workflow

- Always check current branch with `git branch` before creating new branches or committing.
  Never create a feature branch if commits are already on main without first confirming
  the user's branching strategy.
- Verify `gh` CLI availability before attempting to create PRs. If unavailable, provide
  the manual GitHub PR URL instead of failing.

---

## Go Conventions

- Use `context.Background()` (not `context.TODO()`) for top-level contexts.
- Avoid naming variables that shadow package imports.

---

## Import Graph Constraints

- `tools.FileWrite` lives in `internal/tools`, not `internal/models` — moving it would
  create a cycle.
- **Never** import `adapters` from `models`, `pricing`, `runner`, `tools`, `logging`,
  `diff`, or `preferences` — these sit below `adapters` in the dependency graph.

---

## Tool Usage Notes

- The Edit tool may fail on tab-indented Go files. If an edit fails to match, fall back
  to writing the file directly rather than retrying the same edit.
- Avoid `sed` for file edits — consistent issues on macOS. Use Go or direct file writes.

---

## Development Guidelines

- Do not add docs/config for features disabled at compile time or behind off feature flags.
- All TUI rendering (lipgloss, panel layout, diff colors) lives in `internal/ui/` only.
- Tool schemas defined once in `internal/tools/`, translated per-provider in each adapter.
- `internal/diff/` has no external dependencies — keep it that way.
- Preferences are append-only — always `O_APPEND|O_CREATE`, never truncate.
- Never crash on missing API keys — skip the model with a warning.
- Each adapter has a compile-time interface check: `var _ ModelAdapter = (*XAdapter)(nil)`

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

`.env`, `data/` (preferences, logs, history, sessions, checkpoint, configs, pricing cache),
`dist/` (compiled binaries).
