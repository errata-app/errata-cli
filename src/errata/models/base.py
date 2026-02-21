"""Abstract base class for all model adapters."""

from __future__ import annotations

import time
from abc import ABC, abstractmethod
from collections.abc import AsyncIterator
from dataclasses import dataclass


@dataclass
class ModelResponse:
    model_id: str
    text: str
    latency_ms: int
    error: str | None = None

    @property
    def ok(self) -> bool:
        return self.error is None


class ModelAdapter(ABC):
    """Minimal interface every provider adapter must implement."""

    model_id: str  # e.g. "claude-sonnet-4-6"

    @abstractmethod
    def stream(self, prompt: str) -> AsyncIterator[str]:
        """Return an async iterator that yields text chunks from the model."""
        ...

    async def complete(self, prompt: str) -> ModelResponse:
        """Convenience wrapper: collect the full streamed response."""
        start = time.monotonic()
        chunks: list[str] = []
        try:
            async for chunk in self.stream(prompt):
                chunks.append(chunk)
            return ModelResponse(
                model_id=self.model_id,
                text="".join(chunks),
                latency_ms=int((time.monotonic() - start) * 1000),
            )
        except Exception as exc:
            return ModelResponse(
                model_id=self.model_id,
                text="",
                latency_ms=int((time.monotonic() - start) * 1000),
                error=str(exc),
            )
