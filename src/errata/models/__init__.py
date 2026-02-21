"""Model adapters — one per provider."""

from errata.models.base import ModelAdapter, ModelResponse
from errata.models.registry import get_adapter, list_adapters

__all__ = ["ModelAdapter", "ModelResponse", "get_adapter", "list_adapters"]
