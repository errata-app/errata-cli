"""Tests for display helpers (non-interactive, non-Live functions)."""

from __future__ import annotations

import pytest
from io import StringIO
from rich.console import Console

from errata.display import model_color, _make_panels, print_stats
from errata.models.base import ModelResponse


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


# --- _make_panels ---


def test_make_panels_count_matches_model_ids():
    ids = ["a", "b", "c"]
    columns = _make_panels(ids, {m: "" for m in ids}, set(), {})
    # Columns wraps a list of renderables; check via repr
    assert len(columns.renderables) == 3


def test_make_panels_shows_streaming_label_for_in_progress():
    columns = _make_panels(["m"], {"m": ""}, done=set(), latencies={})
    panel = columns.renderables[0]
    assert "streaming" in panel.title


def test_make_panels_shows_latency_when_done():
    columns = _make_panels(["m"], {"m": "hi"}, done={"m"}, latencies={"m": 420})
    panel = columns.renderables[0]
    assert "420ms" in panel.title


def test_make_panels_shows_waiting_for_empty_text():
    columns = _make_panels(["m"], {"m": ""}, done=set(), latencies={})
    panel = columns.renderables[0]
    # Body should be the "waiting…" text — check panel renderable
    from rich.text import Text
    assert isinstance(panel.renderable, Text)


# --- print_stats ---


def _capture_stats(tally: dict) -> str:
    buf = StringIO()
    con = Console(file=buf, highlight=False)
    # Temporarily swap the module-level console
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
    # claude should appear before gpt in output
    assert out.index("claude-sonnet-4-6") < out.index("gpt-4o")


def test_print_stats_win_percentage():
    out = _capture_stats({"a": 1, "b": 1})
    assert "50.0%" in out
