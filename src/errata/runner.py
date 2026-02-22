"""Fan out a prompt to multiple agents concurrently."""

from __future__ import annotations

import asyncio
import time
from collections.abc import Callable

from errata.models.base import AgentEvent, ModelAdapter, ModelResponse

# Per-agent wall-clock timeout. Agents do multi-turn tool loops so this is
# intentionally higher than a single-call timeout.
_AGENT_TIMEOUT_S = 300


async def run_all(
    adapters: list[ModelAdapter],
    prompt: str,
    on_event: Callable[[str, AgentEvent], None],
    verbose: bool = False,
) -> list[ModelResponse]:
    """
    Run `prompt` through all adapters concurrently as tool-using agents.

    Calls `on_event(model_id, event)` as each agent emits tool events or
    (in verbose mode) text chunks.

    Returns a list of ModelResponse in the same order as `adapters`.
    Each agent is subject to a _AGENT_TIMEOUT_S wall-clock timeout.
    """

    async def _run_one(adapter: ModelAdapter) -> ModelResponse:
        start = time.monotonic()
        try:
            response = await asyncio.wait_for(
                adapter.run_agent(
                    prompt,
                    on_event=lambda e: on_event(adapter.model_id, e),
                    verbose=verbose,
                ),
                timeout=_AGENT_TIMEOUT_S,
            )
            # Patch latency in case the adapter didn't set it correctly
            if response.latency_ms == 0:
                response.latency_ms = int((time.monotonic() - start) * 1000)
            return response
        except asyncio.TimeoutError:
            msg = f"timed out after {_AGENT_TIMEOUT_S}s"
            on_event(adapter.model_id, AgentEvent("error", msg))
            return ModelResponse(
                model_id=adapter.model_id,
                text="",
                latency_ms=int((time.monotonic() - start) * 1000),
                error=msg,
            )
        except Exception as exc:
            on_event(adapter.model_id, AgentEvent("error", str(exc)))
            return ModelResponse(
                model_id=adapter.model_id,
                text="",
                latency_ms=int((time.monotonic() - start) * 1000),
                error=str(exc),
            )

    results = await asyncio.gather(*[_run_one(a) for a in adapters])
    return list(results)
