"""Rich-based terminal rendering for Errata."""

from __future__ import annotations

from contextlib import contextmanager
from collections.abc import Callable, Generator

from rich import box
from rich.columns import Columns
from rich.console import Console
from rich.live import Live
from rich.panel import Panel
from rich.table import Table
from rich.text import Text

from errata.models.base import ModelResponse

console = Console()

_COLORS = ["cyan", "magenta", "green", "yellow", "blue", "red"]


def model_color(index: int) -> str:
    return _COLORS[index % len(_COLORS)]


def _make_panels(
    model_ids: list[str],
    texts: dict[str, str],
    done: set[str],
    latencies: dict[str, int],
) -> Columns:
    panels = []
    for i, model_id in enumerate(model_ids):
        color = model_color(i)
        text = texts.get(model_id, "")
        ms = latencies.get(model_id)
        if model_id in done and ms is not None:
            title = f"[bold {color}]{model_id}[/]  [dim]{ms}ms[/]"
        elif model_id in done:
            title = f"[bold {color}]{model_id}[/]"
        else:
            title = f"[bold {color}]{model_id}[/]  [dim]streaming…[/]"
        body = Text(text) if text else Text("waiting…", style="dim")
        panels.append(Panel(body, title=title, border_style=color, expand=True))
    return Columns(panels, equal=True, expand=True)


@contextmanager
def live_stream(model_ids: list[str]) -> Generator[tuple[
    Callable[[str, str], None],   # on_chunk(model_id, chunk)
    Callable[[str, int], None],   # on_done(model_id, latency_ms)
], None, None]:
    """
    Context manager for live-updating streaming panels.

    Yields (on_chunk, on_done) callables to be used as streaming callbacks.
    All calls happen in the asyncio event loop thread, so no locking needed.
    """
    texts: dict[str, str] = {m: "" for m in model_ids}
    done: set[str] = set()
    latencies: dict[str, int] = {}

    def render() -> Columns:
        return _make_panels(model_ids, texts, done, latencies)

    with Live(render(), console=console, refresh_per_second=15, vertical_overflow="visible") as live:
        def on_chunk(model_id: str, chunk: str) -> None:
            texts[model_id] = texts.get(model_id, "") + chunk
            live.update(render())

        def on_done(model_id: str, latency_ms: int) -> None:
            done.add(model_id)
            latencies[model_id] = latency_ms
            live.update(render())

        yield on_chunk, on_done


def print_responses(responses: list[ModelResponse]) -> None:
    """Render completed responses as static panels (used after streaming)."""
    panels = []
    for i, resp in enumerate(responses):
        color = model_color(i)
        label = f"[bold {color}]{resp.model_id}[/]  [dim]{resp.latency_ms}ms[/]"
        body = Text(f"Error: {resp.error}", style="red") if resp.error else Text(resp.text)
        panels.append(Panel(body, title=label, border_style=color, expand=True))
    console.print(Columns(panels, equal=True, expand=True))


def print_selection_prompt(responses: list[ModelResponse]) -> None:
    console.print()
    console.print("[bold]Select a response to apply:[/]")
    for i, resp in enumerate(responses, 1):
        color = model_color(i - 1)
        console.print(f"  [{color}]{i}[/]  {resp.model_id}  [dim]({resp.latency_ms}ms)[/]")
    console.print("  [dim]s[/]  Skip")
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


def warn(message: str) -> None:
    console.print(f"[yellow]Warning:[/] {message}")


def error(message: str) -> None:
    console.print(f"[bold red]Error:[/] {message}")
