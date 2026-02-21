"""Tests for config.Settings and resolved_active_models."""

from __future__ import annotations

import pytest
from errata.config import Settings


def _make_settings(**env) -> Settings:
    """Instantiate Settings with explicit values bypassing env file."""
    return Settings(
        _env_file=None,  # don't read .env during tests
        **env,
    )


def test_defaults_when_no_keys():
    s = _make_settings()
    assert s.anthropic_api_key is None
    assert s.openai_api_key is None
    assert s.google_api_key is None
    assert s.active_models == []


def test_resolved_active_models_uses_explicit_list():
    s = _make_settings(
        anthropic_api_key="sk-ant",
        active_models=["claude-opus-4-6", "claude-sonnet-4-6"],
    )
    assert s.resolved_active_models == ["claude-opus-4-6", "claude-sonnet-4-6"]


def test_resolved_active_models_falls_back_to_anthropic_only():
    s = _make_settings(anthropic_api_key="sk-ant")
    assert s.resolved_active_models == ["claude-sonnet-4-6"]


def test_resolved_active_models_falls_back_to_openai_only():
    s = _make_settings(openai_api_key="sk-oai")
    assert s.resolved_active_models == ["gpt-4o"]


def test_resolved_active_models_falls_back_to_gemini_only():
    s = _make_settings(google_api_key="AIza")
    assert s.resolved_active_models == ["gemini-2.0-flash"]


def test_resolved_active_models_all_providers():
    s = _make_settings(
        anthropic_api_key="sk-ant",
        openai_api_key="sk-oai",
        google_api_key="AIza",
    )
    models = s.resolved_active_models
    assert "claude-sonnet-4-6" in models
    assert "gpt-4o" in models
    assert "gemini-2.0-flash" in models


def test_resolved_active_models_empty_when_no_keys():
    s = _make_settings()
    assert s.resolved_active_models == []


def test_default_model_names():
    s = _make_settings()
    assert s.default_anthropic_model == "claude-sonnet-4-6"
    assert s.default_openai_model == "gpt-4o"
    assert s.default_gemini_model == "gemini-2.0-flash"
