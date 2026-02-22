"""Rich-based terminal rendering for Errata."""

from __future__ import annotations

import difflib
from collections.abc import Callable, Generator
from contextlib import contextmanager
from pathlib import Path

from rich import box
from rich.columns import Columns
from rich.console import Console
from rich.live import Live
from rich.panel import Panel
from rich.rule import Rule
from rich.table import Table
from rich.text import Text

from errata.models.base import AgentEvent, FileWrite, ModelResponse

console = Console()

_COLORS = ["cyan", "magenta", "green", "yellow", "blue", "red"]


def model_color(index: int) -> str:
    return _COLORS[index % len(_COLORS)]


def _make_agent_panels(
    model_ids: list[str],
    events: dict[str, list[AgentEvent]],
    done: set[str],
    latencies: dict[str, int],
) -> Columns:
    panels = []
    for i, model_id in enumerate(model_ids):
        color = model_color(i)
        ms = latencies.get(model_id)
        if model_id in done and ms is not None:
            title = f"[bold {color}]{model_id}[/]  [dim]{ms}ms[/]"
        elif model_id in done:
            title = f"[bold {color}]{model_id}[/]"
        else:
            title = f"[bold {color}]{model_id}[/]  [dim]running…[/]"

        model_events = events.get(model_id, [])
        if not model_events:
            body = Text("waiting…", style="dim")
        else:
            body = Text()
            for ev in model_events[-20:]:  # cap at last 20 lines to avoid panel overflow
                if ev.type == "reading":
                    body.append(f"reading  {ev.data}\n", style="dim cyan")
                elif ev.type == "writing":
                    body.append(f"writing  {ev.data}\n", style="bold yellow")
                elif ev.type == "error":
                    body.append(f"error    {ev.data}\n", style="red")
                elif ev.type == "text":
                    body.append(ev.data, style="dim")

        panels.append(Panel(body, title=title, border_style=color, expand=True))
    return Columns(panels, equal=True, expand=True)


@contextmanager
def live_agent(model_ids: list[str]) -> Generator[tuple[
    Callable[[str, AgentEvent], None],  # on_event(model_id, event)
    Callable[[str, int], None],          # on_done(model_id, latency_ms)
], None, None]:
    """
    Context manager for live-updating agent panels.

    Yields (on_event, on_done) callables. Panels show tool events
    (reading/writing) and optionally text chunks in verbose mode.
    """
    events: dict[str, list[AgentEvent]] = {m: [] for m in model_ids}
    done: set[str] = set()
    latencies: dict[str, int] = {}

    def render() -> Columns:
        return _make_agent_panels(model_ids, events, done, latencies)

    with Live(render(), console=console, refresh_per_second=10, vertical_overflow="visible") as live:
        def on_event(model_id: str, event: AgentEvent) -> None:
            events[model_id].append(event)
            live.update(render())

        def on_done(model_id: str, latency_ms: int) -> None:
            done.add(model_id)
            latencies[model_id] = latency_ms
            live.update(render())

        yield on_event, on_done


_MAX_DIFF_LINES = 20  # max diff lines shown per file


def _render_diff(fw_path: str, new_content: str) -> None:
    """Print a compact colored diff for one proposed write."""
    try:
        old_content = Path(fw_path).read_text(encoding="utf-8")
    except FileNotFoundError:
        old_content = ""

    old_lines = old_content.splitlines()
    new_lines = new_content.splitlines()

    diff = list(difflib.unified_diff(old_lines, new_lines, lineterm="", n=2))

    if not diff:
        console.print(f"    [dim]{fw_path}  (no changes)[/]")
        return

    adds = sum(1 for ln in diff if ln.startswith("+") and not ln.startswith("+++"))
    removes = sum(1 for ln in diff if ln.startswith("-") and not ln.startswith("---"))
    new_file = old_content == ""
    file_label = "[dim](new file)[/] " if new_file else ""
    console.print(f"    [bold]{fw_path}[/]  {file_label}[green]+{adds}[/] [red]-{removes}[/]")

    # Skip the --- +++ header lines, then print up to _MAX_DIFF_LINES
    body_lines = [ln for ln in diff if not ln.startswith("---") and not ln.startswith("+++")]
    shown = 0
    for ln in body_lines:
        if shown >= _MAX_DIFF_LINES:
            remaining = len(body_lines) - shown
            console.print(f"    [dim]… {remaining} more line{'s' if remaining != 1 else ''}[/]")
            break
        if ln.startswith("+"):
            console.print(f"    [green]{ln}[/]")
        elif ln.startswith("-"):
            console.print(f"    [red]{ln}[/]")
        elif ln.startswith("@@"):
            console.print(f"    [dim]{ln}[/]")
        else:
            console.print(f"    [dim]{ln}[/]")
        shown += 1


def print_response_diffs(responses: list[ModelResponse]) -> None:
    """Show a compact diff summary for each model's proposed writes."""
    console.print()
    for i, resp in enumerate(responses):
        color = model_color(i)
        console.print(Rule(
            f"[bold {color}]{resp.model_id}[/]  [dim]{resp.latency_ms}ms[/]",
            style=color,
            align="left",
        ))
        if not resp.proposed_writes:
            # Fall back to showing the first line of the model's explanation
            if resp.text:
                first_line = resp.text.strip().splitlines()[0][:120]
                console.print(f"    [dim]{first_line}[/]")
            else:
                console.print("    [dim](no file writes proposed)[/]")
        else:
            for fw in resp.proposed_writes:
                _render_diff(fw.path, fw.content)
    console.print()


def print_selection_prompt(responses: list[ModelResponse]) -> None:
    console.print()
    console.print("[bold]Select a response to apply:[/]")
    for i, resp in enumerate(responses, 1):
        color = model_color(i - 1)
        writes = resp.proposed_writes
        if writes:
            paths = ", ".join(w.path for w in writes)
            files_label = f"  [dim]→ {paths}[/]"
        else:
            files_label = "  [dim](no file writes proposed)[/]"
        console.print(
            f"  [{color}]{i}[/]  {resp.model_id}  [dim]({resp.latency_ms}ms)[/]{files_label}"
        )
    console.print("  [dim]s[/]  Skip")
    console.print()


def print_apply_summary(writes: list[FileWrite]) -> None:
    for fw in writes:
        console.print(f"[green]Written[/]  [bold]{fw.path}[/]")
    console.print()


def print_stats(tally: dict[str, int]) -> None:
    if not tally:
        console.print("[dim]No preferences recorded yet.[/]")
        return
    table = Table(title="Preference Summary", box=box.ROUNDED)
    table.add_column("Model", style="bold")
    table.add_column("Wins", justify="right")
    table.add_column("Win %", justify="right")
    total = sum(tally.values())
    for model_id, wins in sorted(tally.items(), key=lambda x: -x[1]):
        pct = f"{100 * wins / total:.1f}%"
        table.add_row(model_id, str(wins), pct)
    console.print(table)


def print_banner() -> None:
    console.print(
        Panel(
            "[bold cyan]Errata[/]  [dim]A/B testing tool for agentic AI models[/]",
            border_style="cyan",
            expand=False,
        )
    )


def print_help() -> None:
    table = Table(box=box.SIMPLE, show_header=False, padding=(0, 2))
    table.add_column("Command", style="bold cyan")
    table.add_column("Description", style="dim")
    rows = [
        ("/help", "Show this message"),
        ("/verbose", "Toggle verbose mode (show model text output)"),
        ("/stats", "Preference win summary"),
        ("/models", "List active models"),
        ("/exit  or  /quit", "Exit Errata"),
        ("Ctrl-D", "Exit Errata"),
    ]
    for cmd, desc in rows:
        table.add_row(cmd, desc)
    console.print(table)


def warn(message: str) -> None:
    console.print(f"[yellow]Warning:[/] {message}")


def error(message: str) -> None:
    console.print(f"[bold red]Error:[/] {message}")
