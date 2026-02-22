"""Tests for runner.run_all()."""

from __future__ import annotations

import pytest

from errata.models.base import AgentEvent, FileWrite, ModelAdapter, ModelResponse
from errata.runner import run_all


class _StubAdapter(ModelAdapter):
    def __init__(
        self,
        model_id: str,
        text: str = "",
        writes: list[FileWrite] | None = None,
        events: list[AgentEvent] | None = None,
        *,
        fail: bool = False,
    ):
        self.model_id = model_id
        self._text = text
        self._writes = writes or []
        self._events = events or []
        self._fail = fail

    async def run_agent(
        self,
        prompt: str,
        on_event,
        verbose: bool = False,
    ) -> ModelResponse:
        if self._fail:
            raise RuntimeError(f"{self.model_id} failed")
        for ev in self._events:
            on_event(ev)
        return ModelResponse(
            model_id=self.model_id,
            text=self._text,
            latency_ms=0,
            proposed_writes=self._writes,
        )


@pytest.mark.asyncio
async def test_run_all_returns_correct_order():
    adapters = [
        _StubAdapter("model-a", text="Hello"),
        _StubAdapter("model-b", text="World"),
    ]
    results = await run_all(adapters, "prompt", lambda mid, e: None)

    assert [r.model_id for r in results] == ["model-a", "model-b"]
    assert results[0].text == "Hello"
    assert results[1].text == "World"


@pytest.mark.asyncio
async def test_run_all_calls_on_event():
    ev = AgentEvent("reading", "foo.py")
    adapters = [_StubAdapter("m", events=[ev])]
    received = []
    await run_all(adapters, "p", lambda mid, e: received.append((mid, e)))

    assert received == [("m", ev)]


@pytest.mark.asyncio
async def test_run_all_error_adapter_does_not_affect_others():
    adapters = [
        _StubAdapter("good", text="ok"),
        _StubAdapter("bad", fail=True),
    ]
    results = await run_all(adapters, "p", lambda *_: None)

    good = next(r for r in results if r.model_id == "good")
    bad = next(r for r in results if r.model_id == "bad")

    assert good.ok is True
    assert good.text == "ok"
    assert bad.ok is False
    assert "bad failed" in bad.error


@pytest.mark.asyncio
async def test_run_all_error_surfaces_via_on_event():
    adapters = [_StubAdapter("bad", fail=True)]
    received = []
    await run_all(adapters, "p", lambda mid, e: received.append((mid, e)))

    assert any(e.type == "error" for _, e in received)


@pytest.mark.asyncio
async def test_run_all_latency_recorded():
    adapters = [_StubAdapter("m", text="x")]
    results = await run_all(adapters, "p", lambda *_: None)
    assert results[0].latency_ms >= 0


@pytest.mark.asyncio
async def test_run_all_empty_adapters():
    results = await run_all([], "p", lambda *_: None)
    assert results == []


@pytest.mark.asyncio
async def test_run_all_proposed_writes_preserved():
    writes = [FileWrite("src/foo.py", "content")]
    adapters = [_StubAdapter("m", writes=writes)]
    results = await run_all(adapters, "p", lambda *_: None)
    assert results[0].proposed_writes == writes
