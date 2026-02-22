"""Tests for base dataclasses: ModelResponse, FileWrite, AgentEvent."""

from __future__ import annotations

import pytest

from errata.models.base import AgentEvent, FileWrite, ModelAdapter, ModelResponse

# --- ModelResponse ---


def test_ok_true_when_no_error():
    r = ModelResponse(model_id="m", text="hello", latency_ms=100)
    assert r.ok is True


def test_ok_false_when_error_set():
    r = ModelResponse(model_id="m", text="", latency_ms=100, error="boom")
    assert r.ok is False


def test_ok_false_when_error_empty_string():
    # Empty string error still counts as an error
    r = ModelResponse(model_id="m", text="", latency_ms=100, error="")
    assert r.ok is False


def test_proposed_writes_defaults_to_empty():
    r = ModelResponse(model_id="m", text="hi", latency_ms=0)
    assert r.proposed_writes == []


def test_proposed_writes_stored():
    writes = [FileWrite("a.py", "content")]
    r = ModelResponse(model_id="m", text="hi", latency_ms=0, proposed_writes=writes)
    assert r.proposed_writes == writes


# --- FileWrite ---


def test_file_write_fields():
    fw = FileWrite(path="src/foo.py", content="hello")
    assert fw.path == "src/foo.py"
    assert fw.content == "hello"


# --- AgentEvent ---


def test_agent_event_fields():
    ev = AgentEvent(type="reading", data="src/foo.py")
    assert ev.type == "reading"
    assert ev.data == "src/foo.py"


def test_agent_event_types():
    for t in ("text", "reading", "writing", "error"):
        ev = AgentEvent(type=t, data="x")
        assert ev.type == t


# --- Concrete stub adapter (verifies ABC contract) ---


class _StubAdapter(ModelAdapter):
    def __init__(self, model_id: str):
        self.model_id = model_id

    async def run_agent(self, prompt, on_event, verbose=False) -> ModelResponse:
        return ModelResponse(model_id=self.model_id, text="stub", latency_ms=0)


@pytest.mark.asyncio
async def test_stub_adapter_run_agent():
    adapter = _StubAdapter("stub-model")
    result = await adapter.run_agent("hi", lambda e: None)
    assert result.model_id == "stub-model"
    assert result.text == "stub"
    assert result.ok is True
