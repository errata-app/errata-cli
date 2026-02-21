"""Build model adapters from config."""

from __future__ import annotations

from errata.config import settings
from errata.models.base import ModelAdapter


def get_adapter(model_id: str) -> ModelAdapter:
    """Return the appropriate adapter for a model ID."""
    if model_id.startswith("claude"):
        if not settings.anthropic_api_key:
            raise ValueError(f"ANTHROPIC_API_KEY not set (needed for {model_id})")
        from errata.models.anthropic import AnthropicAdapter
        return AnthropicAdapter(model_id, settings.anthropic_api_key)

    if model_id.startswith(("gpt-", "o1", "o3")):
        if not settings.openai_api_key:
            raise ValueError(f"OPENAI_API_KEY not set (needed for {model_id})")
        from errata.models.openai import OpenAIAdapter
        return OpenAIAdapter(model_id, settings.openai_api_key)

    if model_id.startswith("gemini"):
        if not settings.google_api_key:
            raise ValueError(f"GOOGLE_API_KEY not set (needed for {model_id})")
        from errata.models.gemini import GeminiAdapter
        return GeminiAdapter(model_id, settings.google_api_key)

    raise ValueError(f"Unknown model: {model_id!r}. Cannot determine provider.")


def list_adapters() -> list[ModelAdapter]:
    """Return adapters for all active models, skipping ones with missing keys."""
    adapters = []
    for model_id in settings.resolved_active_models:
        try:
            adapters.append(get_adapter(model_id))
        except ValueError as exc:
            # Warn but don't crash — display layer will show the warning
            import warnings
            warnings.warn(str(exc), stacklevel=2)
    return adapters
