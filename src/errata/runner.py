"""Fan out a prompt to multiple models concurrently with live streaming."""

from __future__ import annotations

import asyncio
import time
from collections.abc import Callable

from errata.models.base import ModelAdapter, ModelResponse


async def stream_all(
    adapters: list[ModelAdapter],
    prompt: str,
    on_chunk: Callable[[str, str], None],
) -> list[ModelResponse]:
    """
    Stream `prompt` to all adapters concurrently.

    Calls `on_chunk(model_id, chunk)` in the event loop thread as each token
    arrives, so the display can update in real time.

    Returns a list of ModelResponse in the same order as `adapters`.
    """

    async def _run_one(adapter: ModelAdapter) -> ModelResponse:
        start = time.monotonic()
        chunks: list[str] = []
        try:
            async for chunk in adapter.stream(prompt):
                chunks.append(chunk)
                on_chunk(adapter.model_id, chunk)
            return ModelResponse(
                model_id=adapter.model_id,
                text="".join(chunks),
                latency_ms=int((time.monotonic() - start) * 1000),
            )
        except Exception as exc:
            on_chunk(adapter.model_id, f"\n[error: {exc}]")
            return ModelResponse(
                model_id=adapter.model_id,
                text="",
                latency_ms=int((time.monotonic() - start) * 1000),
                error=str(exc),
            )

    results = await asyncio.gather(*[_run_one(a) for a in adapters])
    return list(results)
