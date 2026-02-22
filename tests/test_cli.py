"""Tests for CLI entrypoint (main, stats_command)."""

from __future__ import annotations

import sys


def test_main_parses_stats_command(monkeypatch):
    called = []
    monkeypatch.setattr("errata.cli.stats_command", lambda: called.append(True))
    monkeypatch.setattr(sys, "argv", ["errata", "stats"])
    from errata.cli import main
    main()
    assert called == [True]


def test_main_no_args_runs_repl(monkeypatch):
    captured = {}

    async def fake_repl(session_id):
        captured["session_id"] = session_id

    monkeypatch.setattr("errata.cli._repl", fake_repl)
    monkeypatch.setattr("errata.display.print_banner", lambda: None)
    monkeypatch.setattr(sys, "argv", ["errata"])

    import asyncio

    # Patch asyncio.run to just call the coroutine synchronously
    monkeypatch.setattr("errata.cli.asyncio", asyncio)
    from errata.cli import main
    main()
    assert "session_id" in captured
