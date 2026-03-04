# Errata Defaults Reference

What a fresh binary does when no recipe is present.

---

## Models

One default model per provider whose API key is set:

| Provider   | Default model                              | Env var               |
|------------|--------------------------------------------|-----------------------|
| Anthropic  | `claude-sonnet-4-6`                        | `ANTHROPIC_API_KEY`   |
| OpenAI     | `gpt-4o`                                   | `OPENAI_API_KEY`      |
| Google     | `gemini-2.5-flash`                         | `GOOGLE_API_KEY`      |
| Bedrock    | `anthropic.claude-sonnet-4-20250514-v1:0`  | `AWS_REGION`          |
| Azure      | `gpt-4o`                                   | `AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_ENDPOINT` |
| Vertex AI  | `gemini-2.5-flash`                         | `VERTEX_AI_PROJECT` + `VERTEX_AI_LOCATION` |
| OpenRouter | _(none — requires recipe `## Models`)_     | `OPENROUTER_API_KEY`  |
| LiteLLM    | _(none — requires recipe `## Models`)_     | `LITELLM_BASE_URL`   |

Override with recipe `## Models` section.

---

## System Message

Every model receives a single system message built from two parts:

### 1. Tool use guidance

Automatically filtered to only mention tools that are active for the current run. Override entirely with recipe `## Tool Guidance`.

```
Tool use guidance:
- Use list_directory to explore the project structure before reading specific files.
- Use search_files to find files by name pattern (e.g. search_files("**/*.go")).
- Use search_code to find where a function, type, or string is defined or used.
- Use read_file only after you know which file you need. For large files, use offset and limit to page through content.
- Use edit_file for targeted changes to existing files (replaces an exact string). Use write_file only for new files or complete rewrites.
- Use bash to run tests, builds, or any shell command; always provide a clear description.
- Use web_fetch to read documentation, GitHub issues, package READMEs, or any public URL.
- Use web_search for quick factual lookups (definitions, Wikipedia summaries). For specific URLs, use web_fetch directly.
- write_file and edit_file proposals are NOT written to disk immediately — they are queued and applied only if the user selects your response.
```

### 2. User system prompt (default: empty)

Set via recipe `## System Prompt`. Appended after the tool guidance.

---

## Tools

9 built-in tools registered by default:

| Tool | Description |
|------|-------------|
| `read_file` | Read file contents with optional offset/limit pagination (max 2000 lines) |
| `write_file` | Propose writing a file (queued, not applied until selected) |
| `edit_file` | Propose exact-string replacement in a file (queued) |
| `list_directory` | Recursive directory tree (default depth 2, max 5) |
| `search_files` | Glob-based file name search with `**` support |
| `search_code` | Regex content search with optional context lines |
| `bash` | Shell command execution (2-minute default timeout) |
| `web_fetch` | Fetch and clean a public URL (HTML stripped, 50 KB cap) |
| `web_search` | DuckDuckGo instant answers (8 KB cap) |

Restrict to a subset with recipe `## Tools`. Override descriptions with recipe `## Tool Descriptions`.

---

## Tool Output Limits

| Tool | Limit |
|------|-------|
| `read_file` | 2000 lines |
| `bash` | 10,000 bytes |
| `web_fetch` | 50,000 bytes |
| `web_search` | 8,000 bytes |
| `search_code` | 30 second subprocess timeout |
| `list_directory` | depth capped at 5 |

All hardcoded. Recipe `## Output Processing` can add per-tool line/token truncation rules on top.

---

## Context Window Management

| Setting | Default | Recipe path |
|---------|---------|-------------|
| Max history turns | 20 | `## Context` `max_history_turns:` |
| Context strategy | `auto_compact` | `## Context` `strategy:` |
| Auto-compact threshold | 0.80 | `## Context` `compact_threshold:` |
| Task mode | `independent` | `## Context` `task_mode:` |

### Summarization prompt

Used during `/compact` and auto-compaction:

```
Summarize this conversation for context continuity. Preserve:
- All file paths mentioned and their current state
- Decisions made and their rationale
- Errors encountered and how they were resolved
- The current task and its progress
- Code snippets actively being worked on
Discard verbose tool output and abandoned tangents.
Format: Start with "Current task: ..." then list items concisely.
Reply with ONLY the summary.
```

Override with recipe `## Context Summarization Prompt`.

---

## Constraints

| Setting | Default | Recipe path |
|---------|---------|-------------|
| Agent timeout | 5 minutes | `## Constraints` `timeout:` |
| Bash timeout | 2 minutes | `## Constraints` `bash_timeout:` |
| Max steps | Unlimited | `## Constraints` `max_steps:` |

---

## Sandbox

| Setting | Default | Recipe path |
|---------|---------|-------------|
| Filesystem | `unrestricted` | `## Sandbox` `filesystem:` |
| Network | `full` | `## Sandbox` `network:` |
| Allow localhost fetch | `false` | `## Sandbox` `allow_local_fetch:` |

---

## Model Parameters

| Setting | Default | Recipe path |
|---------|---------|-------------|
| Seed | None (provider default) | `## Model Parameters` `seed:` |
| Temperature | Provider default | `## Model Parameters` `temperature:` |
| Max tokens | Provider default | `## Model Parameters` `max_tokens:` |

