"""Tests for display helpers (non-interactive, non-Live functions)."""

from __future__ import annotations

from io import StringIO

from rich.console import Console

from errata.display import _make_agent_panels, model_color, print_stats
from errata.models.base import AgentEvent

# --- model_color ---


def test_model_color_first_is_cyan():
    assert model_color(0) == "cyan"


def test_model_color_wraps_around():
    # 6 colours defined; index 6 should equal index 0
    assert model_color(6) == model_color(0)
    assert model_color(7) == model_color(1)


def test_model_color_returns_string():
    for i in range(12):
        assert isinstance(model_color(i), str)


# --- _make_agent_panels ---


def test_make_agent_panels_count_matches_model_ids():
    ids = ["a", "b", "c"]
    columns = _make_agent_panels(ids, {m: [] for m in ids}, set(), {})
    assert len(columns.renderables) == 3


def test_make_agent_panels_shows_running_label_for_in_progress():
    columns = _make_agent_panels(["m"], {"m": []}, done=set(), latencies={})
    panel = columns.renderables[0]
    assert "running" in panel.title


def test_make_agent_panels_shows_latency_when_done():
    columns = _make_agent_panels(["m"], {"m": []}, done={"m"}, latencies={"m": 420})
    panel = columns.renderables[0]
    assert "420ms" in panel.title


def test_make_agent_panels_shows_waiting_for_empty_events():
    columns = _make_agent_panels(["m"], {"m": []}, done=set(), latencies={})
    panel = columns.renderables[0]
    from rich.text import Text
    assert isinstance(panel.renderable, Text)


def test_make_agent_panels_shows_reading_event():
    events = [AgentEvent("reading", "src/foo.py")]
    columns = _make_agent_panels(["m"], {"m": events}, done=set(), latencies={})
    panel = columns.renderables[0]
    from rich.text import Text
    body = panel.renderable
    assert isinstance(body, Text)
    assert "src/foo.py" in body.plain


def test_make_agent_panels_shows_writing_event():
    events = [AgentEvent("writing", "src/bar.py")]
    columns = _make_agent_panels(["m"], {"m": events}, done=set(), latencies={})
    panel = columns.renderables[0]
    assert "src/bar.py" in panel.renderable.plain


# --- print_stats ---


def _capture_stats(tally: dict) -> str:
    buf = StringIO()
    con = Console(file=buf, highlight=False)
    import errata.display as d
    original = d.console
    d.console = con
    try:
        print_stats(tally)
    finally:
        d.console = original
    return buf.getvalue()


def test_print_stats_empty_tally():
    out = _capture_stats({})
    assert "No preferences" in out


def test_print_stats_shows_model_names():
    out = _capture_stats({"claude-sonnet-4-6": 3, "gpt-4o": 1})
    assert "claude-sonnet-4-6" in out
    assert "gpt-4o" in out


def test_print_stats_sorted_by_wins_descending():
    out = _capture_stats({"gpt-4o": 1, "claude-sonnet-4-6": 5})
    assert out.index("claude-sonnet-4-6") < out.index("gpt-4o")


def test_print_stats_win_percentage():
    out = _capture_stats({"a": 1, "b": 1})
    assert "50.0%" in out
