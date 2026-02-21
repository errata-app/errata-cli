"""Tests for runner.stream_all()."""

from __future__ import annotations

import pytest
from collections.abc import AsyncIterator

from errata.models.base import ModelAdapter, ModelResponse
from errata.runner import stream_all


class _StubAdapter(ModelAdapter):
    def __init__(self, model_id: str, chunks: list[str], *, fail: bool = False):
        self.model_id = model_id
        self._chunks = chunks
        self._fail = fail

    async def stream(self, prompt: str) -> AsyncIterator[str]:
        if self._fail:
            raise RuntimeError(f"{self.model_id} failed")
        for chunk in self._chunks:
            yield chunk


@pytest.mark.asyncio
async def test_stream_all_returns_correct_order():
    adapters = [
        _StubAdapter("model-a", ["Hello"]),
        _StubAdapter("model-b", ["World"]),
    ]
    chunks_received = []
    results = await stream_all(adapters, "prompt", lambda mid, c: chunks_received.append((mid, c)))

    assert [r.model_id for r in results] == ["model-a", "model-b"]
    assert results[0].text == "Hello"
    assert results[1].text == "World"


@pytest.mark.asyncio
async def test_stream_all_calls_on_chunk():
    adapters = [_StubAdapter("m", ["a", "b", "c"])]
    received = []
    await stream_all(adapters, "p", lambda mid, chunk: received.append((mid, chunk)))

    assert received == [("m", "a"), ("m", "b"), ("m", "c")]


@pytest.mark.asyncio
async def test_stream_all_error_adapter_does_not_affect_others():
    adapters = [
        _StubAdapter("good", ["ok"]),
        _StubAdapter("bad", [], fail=True),
    ]
    results = await stream_all(adapters, "p", lambda *_: None)

    good = next(r for r in results if r.model_id == "good")
    bad = next(r for r in results if r.model_id == "bad")

    assert good.ok is True
    assert good.text == "ok"
    assert bad.ok is False
    assert "bad failed" in bad.error


@pytest.mark.asyncio
async def test_stream_all_error_chunk_appended_to_on_chunk():
    """Error message should be surfaced via on_chunk so the display updates."""
    adapters = [_StubAdapter("bad", [], fail=True)]
    received = []
    await stream_all(adapters, "p", lambda mid, c: received.append((mid, c)))

    assert any("error" in c for _, c in received)


@pytest.mark.asyncio
async def test_stream_all_latency_recorded():
    adapters = [_StubAdapter("m", ["x"])]
    results = await stream_all(adapters, "p", lambda *_: None)
    assert results[0].latency_ms >= 0


@pytest.mark.asyncio
async def test_stream_all_empty_adapters():
    results = await stream_all([], "p", lambda *_: None)
    assert results == []
