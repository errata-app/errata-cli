"""Tests for models/registry.py."""

from __future__ import annotations

import pytest

import errata.models.registry as registry_module
from errata.models.anthropic import AnthropicAdapter
from errata.models.openai import OpenAIAdapter
from errata.models.gemini import GeminiAdapter


@pytest.fixture(autouse=True)
def clear_key_defaults(monkeypatch):
    """Start each test with no API keys set."""
    monkeypatch.setattr(registry_module.settings, "anthropic_api_key", None)
    monkeypatch.setattr(registry_module.settings, "openai_api_key", None)
    monkeypatch.setattr(registry_module.settings, "google_api_key", None)
    monkeypatch.setattr(registry_module.settings, "active_models", [])


# --- get_adapter ---


def test_get_adapter_returns_anthropic(monkeypatch):
    monkeypatch.setattr(registry_module.settings, "anthropic_api_key", "sk-ant-test")
    adapter = registry_module.get_adapter("claude-sonnet-4-6")
    assert isinstance(adapter, AnthropicAdapter)
    assert adapter.model_id == "claude-sonnet-4-6"


def test_get_adapter_raises_without_anthropic_key():
    with pytest.raises(ValueError, match="ANTHROPIC_API_KEY"):
        registry_module.get_adapter("claude-sonnet-4-6")


def test_get_adapter_returns_openai(monkeypatch):
    monkeypatch.setattr(registry_module.settings, "openai_api_key", "sk-test")
    adapter = registry_module.get_adapter("gpt-4o")
    assert isinstance(adapter, OpenAIAdapter)
    assert adapter.model_id == "gpt-4o"


def test_get_adapter_raises_without_openai_key():
    with pytest.raises(ValueError, match="OPENAI_API_KEY"):
        registry_module.get_adapter("gpt-4o")


def test_get_adapter_returns_gemini(monkeypatch):
    monkeypatch.setattr(registry_module.settings, "google_api_key", "AIza-test")
    adapter = registry_module.get_adapter("gemini-2.0-flash")
    assert isinstance(adapter, GeminiAdapter)
    assert adapter.model_id == "gemini-2.0-flash"


def test_get_adapter_raises_without_gemini_key():
    with pytest.raises(ValueError, match="GOOGLE_API_KEY"):
        registry_module.get_adapter("gemini-2.0-flash")


def test_get_adapter_raises_for_unknown_model():
    with pytest.raises(ValueError, match="Unknown model"):
        registry_module.get_adapter("llama-3-unknown")


# --- list_adapters ---


def test_list_adapters_returns_adapters_for_active_models(monkeypatch):
    monkeypatch.setattr(registry_module.settings, "anthropic_api_key", "sk-ant-test")
    monkeypatch.setattr(registry_module.settings, "active_models", ["claude-sonnet-4-6"])
    adapters = registry_module.list_adapters()
    assert len(adapters) == 1
    assert isinstance(adapters[0], AnthropicAdapter)


def test_list_adapters_skips_models_with_missing_keys(monkeypatch):
    # anthropic key present, openai key absent
    monkeypatch.setattr(registry_module.settings, "anthropic_api_key", "sk-ant-test")
    monkeypatch.setattr(registry_module.settings, "active_models", ["claude-sonnet-4-6", "gpt-4o"])
    import warnings
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        adapters = registry_module.list_adapters()
    assert len(adapters) == 1
    assert isinstance(adapters[0], AnthropicAdapter)
    assert any("OPENAI_API_KEY" in str(w.message) for w in caught)


def test_list_adapters_returns_empty_when_no_keys():
    adapters = registry_module.list_adapters()
    assert adapters == []
