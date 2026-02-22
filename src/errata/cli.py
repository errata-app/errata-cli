"""Main CLI entrypoint — interactive REPL loop."""

from __future__ import annotations

import argparse
import asyncio
import sys
import uuid
import warnings

from prompt_toolkit import PromptSession
from prompt_toolkit.history import FileHistory
from prompt_toolkit.styles import Style

import errata.display as display
import errata.preferences as preferences
from errata.models.base import ModelResponse
from errata.models.registry import list_adapters
from errata.runner import run_all
from errata.tools import apply_writes

_HISTORY_FILE = ".errata_history"

_STYLE = Style.from_dict({"prompt": "bold cyan"})


def _collect_adapters():
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        adapters = list_adapters()
    for w in caught:
        display.warn(str(w.message))
    return adapters


async def _repl(session_id: str) -> None:
    session = PromptSession(
        history=FileHistory(_HISTORY_FILE),
        style=_STYLE,
    )
    adapters = _collect_adapters()

    if not adapters:
        display.error(
            "No models available. Set at least one API key in your .env:\n"
            "  ANTHROPIC_API_KEY=sk-ant-...\n"
            "  OPENAI_API_KEY=sk-...\n"
            "  GOOGLE_API_KEY=AIza..."
        )
        sys.exit(1)

    model_ids = [a.model_id for a in adapters]
    verbose = False

    display.console.print(f"[dim]Models: {', '.join(model_ids)}[/]")
    display.console.print("[dim]Ctrl-D or /exit to quit  •  /help for commands[/]\n")

    while True:
        try:
            prompt_text = await session.prompt_async([("class:prompt", "errata> ")])
        except (EOFError, KeyboardInterrupt):
            display.console.print("\n[dim]Goodbye.[/]")
            break

        prompt_text = prompt_text.strip()
        if not prompt_text:
            continue
        if prompt_text in ("/exit", "/quit", "exit", "quit"):
            display.console.print("[dim]Goodbye.[/]")
            break
        if prompt_text == "/help":
            display.print_help()
            continue
        if prompt_text == "/stats":
            display.print_stats(preferences.summarize())
            continue
        if prompt_text == "/models":
            display.console.print(f"[dim]{', '.join(model_ids)}[/]")
            continue
        if prompt_text == "/verbose":
            verbose = not verbose
            state = "on" if verbose else "off"
            display.console.print(f"[dim]Verbose mode {state}.[/]")
            continue

        # --- run all models as agents ---
        responses: list[ModelResponse] = []
        with display.live_agent(model_ids) as (on_event, on_done):
            async def _run() -> None:
                nonlocal responses
                responses = await run_all(adapters, prompt_text, on_event, verbose=verbose)
                for r in responses:
                    on_done(r.model_id, r.latency_ms)

            await _run()

        ok_responses = [r for r in responses if r.ok]
        if not ok_responses:
            display.warn("All models returned errors.")
            continue

        display.print_response_diffs(ok_responses)
        display.print_selection_prompt(ok_responses)

        try:
            choice_raw = await session.prompt_async([("class:prompt", "choice> ")])
        except (EOFError, KeyboardInterrupt):
            display.console.print("\n[dim]Goodbye.[/]")
            break

        choice_raw = choice_raw.strip().lower()
        if choice_raw in ("s", ""):
            display.console.print("[dim]Skipped.[/]\n")
            continue

        try:
            idx = int(choice_raw) - 1
            if not (0 <= idx < len(ok_responses)):
                raise ValueError
        except ValueError:
            display.warn("Invalid choice — skipped.\n")
            continue

        selected = ok_responses[idx]

        if not selected.proposed_writes:
            display.warn("Model proposed no file writes.")
        else:
            apply_writes(selected.proposed_writes)
            display.print_apply_summary(selected.proposed_writes)

        preferences.record(
            prompt=prompt_text,
            responses=responses,
            selected_model=selected.model_id,
            session_id=session_id,
        )


def stats_command() -> None:
    display.print_banner()
    display.print_stats(preferences.summarize())


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="errata",
        description="A/B testing tool for agentic AI models",
    )
    parser.add_argument(
        "command",
        nargs="?",
        choices=["stats"],
        help="stats — show preference summary",
    )
    args = parser.parse_args()

    if args.command == "stats":
        stats_command()
        return

    display.print_banner()
    asyncio.run(_repl(session_id=str(uuid.uuid4())))


if __name__ == "__main__":
    main()
