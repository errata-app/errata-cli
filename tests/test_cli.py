"""Tests for CLI helpers (_apply, stats_command, argument parsing)."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

from errata.cli import _apply
from errata.models.base import ModelResponse


# --- _apply ---


def test_apply_writes_to_file(tmp_path):
    target = tmp_path / "output.py"
    response = ModelResponse(model_id="claude-sonnet-4-6", text="def foo(): pass", latency_ms=100)
    _apply(response, target)
    assert target.read_text(encoding="utf-8") == "def foo(): pass"


def test_apply_overwrites_existing_file(tmp_path):
    target = tmp_path / "output.py"
    target.write_text("old content", encoding="utf-8")
    response = ModelResponse(model_id="m", text="new content", latency_ms=50)
    _apply(response, target)
    assert target.read_text(encoding="utf-8") == "new content"


def test_apply_creates_file_if_not_exists(tmp_path):
    target = tmp_path / "subdir" / "new.py"
    target.parent.mkdir(parents=True)
    response = ModelResponse(model_id="m", text="hello", latency_ms=10)
    _apply(response, target)
    assert target.exists()


def test_apply_clipboard_when_no_file(monkeypatch):
    copied = []
    monkeypatch.setitem(sys.modules, "pyperclip", type(sys)("pyperclip"))
    import types
    fake_pyperclip = types.ModuleType("pyperclip")
    fake_pyperclip.copy = lambda text: copied.append(text)
    monkeypatch.setitem(sys.modules, "pyperclip", fake_pyperclip)

    response = ModelResponse(model_id="m", text="clipboard text", latency_ms=10)
    _apply(response, target_file=None)
    assert copied == ["clipboard text"]


def test_apply_falls_back_to_print_when_pyperclip_unavailable(monkeypatch, capsys):
    import types
    fake_pyperclip = types.ModuleType("pyperclip")

    def _raise(text):
        raise Exception("no clipboard")

    fake_pyperclip.copy = _raise
    monkeypatch.setitem(sys.modules, "pyperclip", fake_pyperclip)

    response = ModelResponse(model_id="m", text="fallback text", latency_ms=10)
    # Should not raise; output goes to console
    _apply(response, target_file=None)


# --- main() argument parsing ---


def test_main_parses_stats_command(monkeypatch):
    called = []
    monkeypatch.setattr("errata.cli.stats_command", lambda: called.append(True))
    monkeypatch.setattr(sys, "argv", ["errata", "stats"])
    from errata.cli import main
    main()
    assert called == [True]


def test_main_parses_file_flag(monkeypatch, tmp_path):
    captured = {}

    async def fake_repl(session_id, target_file):
        captured["target_file"] = target_file

    monkeypatch.setattr("errata.cli._repl", fake_repl)
    monkeypatch.setattr("errata.display.print_banner", lambda: None)

    target = tmp_path / "out.py"
    monkeypatch.setattr(sys, "argv", ["errata", "--file", str(target)])

    from errata.cli import main
    main()
    assert captured["target_file"] == target


def test_main_no_file_passes_none(monkeypatch):
    captured = {}

    async def fake_repl(session_id, target_file):
        captured["target_file"] = target_file

    monkeypatch.setattr("errata.cli._repl", fake_repl)
    monkeypatch.setattr("errata.display.print_banner", lambda: None)
    monkeypatch.setattr(sys, "argv", ["errata"])

    from errata.cli import main
    main()
    assert captured["target_file"] is None
