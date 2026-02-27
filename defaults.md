# Errata Defaults Reference

What a fresh binary sends to models and how it behaves when no recipe is imported.

---

## Models

One default model is activated per provider whose API key is present in `.env`:

| Provider   | Default model                              | Env var               |
|------------|--------------------------------------------|-----------------------|
| Anthropic  | `claude-sonnet-4-6`                        | `ANTHROPIC_API_KEY`   |
| OpenAI     | `gpt-4o`                                   | `OPENAI_API_KEY`      |
| Google     | `gemini-2.0-flash`                         | `GOOGLE_API_KEY`      |
| Bedrock    | `anthropic.claude-sonnet-4-20250514-v1:0`  | `AWS_REGION`          |
| Azure      | `gpt-4o`                                   | `AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_ENDPOINT` |
| Vertex AI  | `gemini-2.0-flash`                         | `VERTEX_AI_PROJECT` + `VERTEX_AI_LOCATION` |
| OpenRouter | _(none — requires recipe `## Models`)_     | `OPENROUTER_API_KEY`  |
| LiteLLM    | _(none — requires recipe `## Models`)_     | `LITELLM_BASE_URL`   |

Override with recipe `## Models` section.

---

## System Message

Every model receives a single system message assembled from two parts:

### 1. Tool use guidance (default, overridable via recipe `## Tool Guidance`)

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

~200 tokens. Override with recipe `## Tool Guidance` or `/config` tool-guidance section.
When overridden, the entire block above is replaced with the custom text.

### 2. User system prompt (from recipe, default: empty)

Set via recipe `## System Prompt`. Appended directly after the tool guidance.
With no recipe, nothing is appended — models receive only the guidance above.

---

## Tools

9 built-in tools are registered by default. Each tool definition (name, description,
parameter schemas) is sent to the model as part of the API call.

| Tool | Description (sent to model) |
|------|----------------------------|
| `read_file` | Read file contents with optional offset/limit pagination (max 2000 lines) |
| `write_file` | Propose writing a file (queued, not applied until user selects) |
| `edit_file` | Propose exact-string replacement in a file (queued) |
| `list_directory` | Recursive directory tree (default depth 2, max 5) |
| `search_files` | Glob-based file name search with `**` support |
| `search_code` | Regex content search with optional context lines |
| `bash` | Shell command execution (2-minute default timeout) |
| `web_fetch` | Fetch and clean a public URL (HTML → plain text, 50 KB cap) |
| `web_search` | DuckDuckGo instant answers (knowledge panels, 8 KB cap) |

Tool descriptions are hardcoded but can be overridden per-tool via recipe `## Tool Descriptions`.
Tools can be restricted to a subset via recipe `## Tools` (allowlist).
Tools can be disabled per-session with `/tools off <name>`.

---

## Tool Output Limits

These caps apply to tool results returned to the model:

| Tool | Limit | Configurable |
|------|-------|-------------|
| `read_file` | 2000 lines | No (hardcoded `maxReadLines`) |
| `bash` | 10,000 bytes | No (hardcoded `bashOutputLimit`) |
| `web_fetch` | 50,000 bytes | No (hardcoded `webFetchOutputLimit`) |
| `web_search` | 8,000 bytes | No (hardcoded `webSearchOutputLimit`) |
| `search_code` | 30 second timeout | No (hardcoded `searchCommandTimeout`) |
| `list_directory` | depth capped at 5 | No (hardcoded) |
| All tools | Per-tool line limits | Yes — recipe `## Output Processing` rules |

When output exceeds a limit, a truncation notice is appended:
`[truncated: output exceeded N bytes]` (hardcoded format for byte limits)
or a configurable `truncation_message` template for line-based `## Output Processing` rules.

---

## Tool Result Strings

These hardcoded strings are returned to the model as tool call results:

| Context | String |
|---------|--------|
| After `write_file` or `edit_file` | `"Write queued — will be applied if selected."` |
| After compaction | User turn: `"[Previous conversation — compacted]"` |

---

## Context Window Management

| Setting | Default | Recipe path |
|---------|---------|-------------|
| Max history turns | 20 | `## Context` `max_history_turns:` |
| Context strategy | `auto_compact` | `## Context` `strategy:` |
| Auto-compact threshold | 0.80 (80% fill) | `## Context` `compact_threshold:` |

### Summarization prompt (used during compaction)

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

Override via recipe `## Context Summarization Prompt`.

---

## Constraints

| Setting | Default | Recipe path |
|---------|---------|-------------|
| Agent timeout | 5 minutes | `## Constraints` `timeout:` |
| Bash timeout | 2 minutes | `## Constraints` `bash_timeout:` |
| Max steps (tool calls) | Unlimited | `## Constraints` `max_steps:` |

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

---

## Features Disabled by Default

| Feature | Gate | How to enable |
|---------|------|---------------|
| Sub-agents (`spawn_agent`) | `tools.SubagentEnabled = false` | Change constant to `true` in `internal/tools/tools.go` and recompile |
| MCP servers | No servers configured | Add recipe `## MCP Servers` section |
| System reminders | None configured | Add recipe `## System Reminders` section |
| Hooks | None configured | Add recipe `## Hooks` section |
| Output processing | No rules | Add recipe `## Output Processing` section |

---

## What Models Do NOT Receive by Default

- No custom system prompt (recipe `## System Prompt` is empty)
- No tool description overrides
- No MCP tool definitions
- No `spawn_agent` tool, guidance, or error messages
- No conversation history (empty on first prompt)
- No system reminders or hooks output
- No output processing truncation (no rules configured)
