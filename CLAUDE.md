# Errata — A/B Testing Tool for Agentic AI Models

## Project Overview

**Errata** is a CLI tool (modeled after Claude Code's terminal UX) that sends programming prompts to multiple AI models simultaneously, presents responses for comparison, lets the user select their preferred output, applies it, and logs preferences for later analysis.

## Stack

- **Language:** Python 3.11+
- **Package manager:** `uv` (preferred) or `pip`
- **CLI/TUI:** `rich` + `prompt_toolkit` for a Claude Code-style interactive terminal experience
- **AI SDKs:** `anthropic`, `openai`, `google-generativeai`
- **Config:** `pydantic-settings` + `.env` for API keys and model config
- **Preferences storage:** append-only JSONL at `data/preferences.jsonl`

## Architecture

```
src/errata/
├── cli.py            # Entrypoint — interactive REPL loop
├── runner.py         # Sends prompt to N models concurrently (asyncio)
├── display.py        # Rich-based terminal rendering (panels, diffs, selection UI)
├── preferences.py    # Records and queries user preference history
├── config.py         # Pydantic settings — API keys, active models, defaults
└── models/
    ├── base.py       # Abstract ModelAdapter (stream + complete)
    ├── anthropic.py  # Claude (Opus, Sonnet, Haiku via Anthropic SDK)
    ├── openai.py     # GPT-4o, o1, etc. via OpenAI SDK
    └── gemini.py     # Gemini 2.x via google-generativeai
```

## Key Commands (dev)

```bash
# Install dependencies
uv sync

# Run the tool
uv run errata

# Run with a specific set of models for this session
uv run errata --models claude-opus-4-6,claude-sonnet-4-6

# View preference analysis
uv run errata stats

# Run tests
uv run pytest
```

## Core Workflow

1. User types a prompt (or pastes code + instruction) in the Errata REPL
2. `runner.py` fans out to all configured models concurrently via `asyncio`
3. Responses stream in and are displayed in labeled panels (one per model)
4. User selects preferred response with arrow keys + Enter (or a key shortcut)
5. Selected response is copied to clipboard / written to stdout
6. Preference is recorded: `{timestamp, prompt_hash, models, selected, latencies}`

## Model Configuration

Models are configured in `.env` or `errata.toml`. Any model can be toggled on/off.

```toml
# errata.toml (optional override)
[models]
active = ["claude-opus-4-6", "claude-sonnet-4-6", "gpt-4o"]
```

## Preference Schema (JSONL)

```json
{
  "ts": "2026-02-21T10:00:00Z",
  "prompt_hash": "sha256:...",
  "prompt_preview": "first 120 chars...",
  "models": ["claude-opus-4-6", "claude-sonnet-4-6"],
  "selected": "claude-opus-4-6",
  "latencies_ms": {"claude-opus-4-6": 1200, "claude-sonnet-4-6": 800},
  "session_id": "uuid"
}
```

## Development Guidelines

- Use `asyncio` throughout — all model calls are async/streaming
- Each `ModelAdapter` must implement `stream(prompt) -> AsyncIterator[str]`
- Keep display logic in `display.py`; no Rich imports elsewhere
- Preferences are append-only — never modify `preferences.jsonl`, only append
- API keys live in `.env` only — never commit them
- Fail gracefully: if a model's API key is missing, skip it and warn the user
- Use `uv` for all package operations

## Files to Never Commit

- `.env`
- `data/preferences.jsonl` (contains prompt history)
