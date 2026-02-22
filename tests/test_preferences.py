"""Tests for the preference log."""

import hashlib

import pytest

from errata.models.base import ModelResponse


@pytest.fixture(autouse=True)
def tmp_prefs(tmp_path, monkeypatch):
    """Redirect preferences to a temp file for each test."""
    monkeypatch.setattr("errata.preferences.settings.preferences_path", str(tmp_path / "prefs.jsonl"))


def test_record_and_load():
    from errata import preferences

    responses = [
        ModelResponse(model_id="claude-sonnet-4-6", text="Hello", latency_ms=100),
        ModelResponse(model_id="gpt-4o", text="Hi there", latency_ms=200),
    ]
    preferences.record("Say hello", responses, selected_model="claude-sonnet-4-6")

    records = preferences.load_all()
    assert len(records) == 1
    assert records[0]["selected"] == "claude-sonnet-4-6"
    assert records[0]["models"] == ["claude-sonnet-4-6", "gpt-4o"]


def test_summarize():
    from errata import preferences

    responses = [
        ModelResponse(model_id="claude-sonnet-4-6", text="a", latency_ms=100),
        ModelResponse(model_id="gpt-4o", text="b", latency_ms=200),
    ]
    preferences.record("prompt 1", responses, selected_model="claude-sonnet-4-6")
    preferences.record("prompt 2", responses, selected_model="gpt-4o")
    preferences.record("prompt 3", responses, selected_model="claude-sonnet-4-6")

    tally = preferences.summarize()
    assert tally["claude-sonnet-4-6"] == 2
    assert tally["gpt-4o"] == 1


def test_load_all_returns_empty_when_file_missing():
    from errata import preferences
    assert preferences.load_all() == []


def test_record_prompt_hash():
    from errata import preferences
    prompt = "write a sort function"
    responses = [ModelResponse(model_id="m", text="x", latency_ms=10)]
    preferences.record(prompt, responses, selected_model="m")
    record = preferences.load_all()[0]
    expected = "sha256:" + hashlib.sha256(prompt.encode()).hexdigest()
    assert record["prompt_hash"] == expected


def test_record_prompt_preview_truncated():
    from errata import preferences
    long_prompt = "x" * 200
    responses = [ModelResponse(model_id="m", text="y", latency_ms=10)]
    preferences.record(long_prompt, responses, selected_model="m")
    record = preferences.load_all()[0]
    assert len(record["prompt_preview"]) == 120


def test_record_uses_provided_session_id():
    from errata import preferences
    responses = [ModelResponse(model_id="m", text="y", latency_ms=10)]
    preferences.record("p", responses, selected_model="m", session_id="my-session")
    record = preferences.load_all()[0]
    assert record["session_id"] == "my-session"


def test_record_generates_session_id_when_none():
    from errata import preferences
    responses = [ModelResponse(model_id="m", text="y", latency_ms=10)]
    preferences.record("p", responses, selected_model="m", session_id=None)
    record = preferences.load_all()[0]
    assert record["session_id"]  # non-empty UUID


def test_record_latencies_map():
    from errata import preferences
    responses = [
        ModelResponse(model_id="a", text="x", latency_ms=111),
        ModelResponse(model_id="b", text="y", latency_ms=222),
    ]
    preferences.record("p", responses, selected_model="a")
    record = preferences.load_all()[0]
    assert record["latencies_ms"] == {"a": 111, "b": 222}


def test_record_is_append_only():
    from errata import preferences
    responses = [ModelResponse(model_id="m", text="x", latency_ms=10)]
    preferences.record("first", responses, selected_model="m")
    preferences.record("second", responses, selected_model="m")
    records = preferences.load_all()
    assert len(records) == 2
    assert records[0]["prompt_preview"] == "first"
    assert records[1]["prompt_preview"] == "second"


def test_summarize_returns_empty_when_no_records():
    from errata import preferences
    assert preferences.summarize() == {}
