# Errata

Compare AI models on real tasks. Same prompt, same tools, different models.

## Install

```bash
git clone https://github.com/suarezc/Errata.git
cd Errata
go build -o errata ./cmd/errata
```

or

```bash
go install github.com/suarezc/errata/cmd/errata@latest
```

## Quick Start

Set your API keys in a `.env` file:

```bash
ANTHROPIC_API_KEY=sk-ant-...
OPENROUTER_API_KEY=sk-or-...
GOOGLE_API_KEY=AI...
OPENAI_API_KEY=sk...
```

The example recipe assumes the OpenRouter naming convention Errata uses for models. If you are running provider APIs directly you may be able to remove the */ before the model names.

Run the example recipe:

```bash
./errata run -r go_docstore.md --verbose
```
or

```bash
errata run -r go_docstore.md --verbose
```

Sample output:

```
errata: DocStore Bug Fix Challenge (1 task, 6 models, task_mode=isolated)

[1/1] The Go project at go_gauntlet_test/challenge11_docstore/ has failing te...
    [claude-sonnet-4-6] bash: cd go_gauntlet_test/challenge11_docstore && go test -v ./...
    [claude-sonnet-4-6] reading: go_gauntlet_test/challenge11_docstore/collection.go
    [claude-sonnet-4-6] reading: go_gauntlet_test/challenge11_docstore/query.go
    [claude-sonnet-4-6] writing: go_gauntlet_test/challenge11_docstore/collection.go
    [claude-sonnet-4-6] writing: go_gauntlet_test/challenge11_docstore/index.go
    [claude-sonnet-4-6] bash: cd go_gauntlet_test/challenge11_docstore && go test -v ./...
    [gemini-2.5-pro] bash: cd go_gauntlet_test/challenge11_docstore && go test -v ./...
    [gemini-2.5-pro] reading: go_gauntlet_test/challenge11_docstore/document.go
    ...
  claude-sonnet-4-6      PASS   12804ms  $0.0891  4/4 criteria
  gemini-2.5-pro         PASS   18443ms  $0.0467  4/4 criteria
  o3                     PASS   21587ms  $0.1203  4/4 criteria
  gpt-4.1                FAIL   15872ms  $0.0312  2/4 criteria
  claude-haiku-4.5       FAIL    9241ms  $0.0038  1/4 criteria
  llama-3.1-8b-instruct  FAIL    6102ms  $0.0004  0/4 criteria

Summary: 1 task, $0.2915 total cost
Report saved to data/outputs/rpt_019cba97.json
```

## Write Your Own Recipe

Create a Markdown file with these sections:

```markdown
## Models
<!-- Which models to test -->
- claude-sonnet-4-6
- openai/gpt-4o
- google/gemini-2.5-flash

## System Prompt
<!-- Instructions given to every model -->
You are a senior Go developer. Always run tests before proposing changes.

## Tools
<!-- Which tools models can use, empty section for none, omit section entirely for all -->
- read_file
- edit_file
- bash
- search_code

## Tasks
<!-- The prompts to send to each model -->
- Write a CLI that fetches weather data from an API
- Refactor the handler to use dependency injection

## Success Criteria
<!-- How to score each response -->
- run: go build ./...
- run: go test ./...
- file_exists: main.go
```

Then run it:

```bash
errata run -r my-recipe.md
```

## Interactive Mode (TUI)

Start the TUI with `errata` (no subcommand). Type a prompt, and every configured model works on it concurrently. Live panels show each model's tool activity. When they finish, pick the best response — its file writes are applied to disk and your choice is logged to `data/preferences.jsonl`.

```bash
errata                           # fresh session
errata --continue                # resume most recent session
errata --resume <id>             # resume a specific session
```

### Key commands

| Command | What it does |
|---------|-------------|
| `/config` | Browse and edit the session recipe (models, tools, constraints, system prompt) |
| `/stats` | Show model win counts and session cost |
| `/compact` | Summarize conversation history to free context window |
| `/resume` | Re-run only interrupted models from a cancelled run |
| `/rewind` | Undo the last run (revert writes and remove from context) |
| `/save [path]` | Save the session recipe to disk |
| `/load <path>` | Load a recipe file into the session |
| `/verbose` | Toggle showing model text alongside tool events |
| `/help` | Show all commands |


### Data and output

Errata stores all data under `data/`:

| Path | Contents |
|------|----------|
| `data/preferences.jsonl` | Every model selection (append-only) |
| `data/outputs/` | JSON reports from headless runs and `/export` |
| `data/sessions/` | Per-session history, feed, and recipe state |
| `data/prompt_history.jsonl` | Prompt recall for Up-arrow / Ctrl-R |

View a summary with `/stats` in the TUI or `errata stats` from the command line. Filter by recipe with `errata stats --recipe <name>`.

#### Querying with jq examples

Win counts per model:

```bash
jq -s 'group_by(.selected) | map({model: .[0].selected, wins: length}) | sort_by(-.wins)' data/preferences.jsonl
```

All models that passed every criterion in a headless report:

```bash
jq '.tasks[].criteria_results | to_entries[] | select(.value | all(.passed)) | .key' data/outputs/*.json
```

Per-model pass rate from a headless report:

```bash
jq '.summary.per_model | to_entries[] | "\(.key): \(.value.criteria_passed)/\(.value.criteria_total)"' data/outputs/*.json
```

---

--- Below are features intended for full release, they do not exist as of now. ---
## Commands

| Command | Description |
|---------|-------------|
| `errata publish` | Upload a recipe to errata.app |
| `errata pull <id>` | Download a community recipe |

## Community

- **Browse and share recipes:** [errata.app](https://errata.app)
- **Report issues:** [GitHub Issues](https://github.com/suarezc/errata/issues)
