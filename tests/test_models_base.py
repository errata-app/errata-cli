"""Tests for ModelResponse and ModelAdapter.complete()."""

from __future__ import annotations

import pytest
from collections.abc import AsyncIterator

from errata.models.base import ModelAdapter, ModelResponse


# --- ModelResponse ---


def test_ok_true_when_no_error():
    r = ModelResponse(model_id="m", text="hello", latency_ms=100)
    assert r.ok is True


def test_ok_false_when_error_set():
    r = ModelResponse(model_id="m", text="", latency_ms=100, error="boom")
    assert r.ok is False


def test_ok_false_when_error_empty_string():
    # Empty string is still truthy enough to mark as failed
    r = ModelResponse(model_id="m", text="", latency_ms=100, error="")
    assert r.ok is False


# --- Concrete stub adapter ---


class _StubAdapter(ModelAdapter):
    """Yields a fixed list of chunks; raises on demand."""

    def __init__(self, model_id: str, chunks: list[str], *, raise_on: str | None = None):
        self.model_id = model_id
        self._chunks = chunks
        self._raise_on = raise_on

    async def stream(self, prompt: str) -> AsyncIterator[str]:
        for chunk in self._chunks:
            if self._raise_on and chunk == self._raise_on:
                raise RuntimeError(f"forced error at chunk {chunk!r}")
            yield chunk


# --- ModelAdapter.complete() ---


@pytest.mark.asyncio
async def test_complete_joins_chunks():
    adapter = _StubAdapter("test-model", ["Hello", ", ", "world"])
    result = await adapter.complete("hi")
    assert result.text == "Hello, world"
    assert result.model_id == "test-model"
    assert result.error is None
    assert result.ok is True


@pytest.mark.asyncio
async def test_complete_empty_stream():
    adapter = _StubAdapter("test-model", [])
    result = await adapter.complete("hi")
    assert result.text == ""
    assert result.ok is True


@pytest.mark.asyncio
async def test_complete_records_latency():
    adapter = _StubAdapter("test-model", ["a", "b"])
    result = await adapter.complete("hi")
    assert result.latency_ms >= 0


@pytest.mark.asyncio
async def test_complete_captures_exception():
    adapter = _StubAdapter("test-model", ["a", "ERR", "b"], raise_on="ERR")
    result = await adapter.complete("hi")
    assert result.ok is False
    assert "forced error" in result.error
    assert result.text == ""
    assert result.latency_ms >= 0
