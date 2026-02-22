"""Anthropic (Claude) model adapter."""

from __future__ import annotations

import time
from collections.abc import Callable

from errata.models.base import AgentEvent, FileWrite, ModelAdapter, ModelResponse
from errata.tools import READ_TOOL_NAME, TOOL_DEFINITIONS, WRITE_TOOL_NAME, execute_read

# Translate canonical tool defs to Anthropic's input_schema format
_TOOLS = [
    {
        "name": t["name"],
        "description": t["description"],
        "input_schema": t["parameters"],
    }
    for t in TOOL_DEFINITIONS
]


class AnthropicAdapter(ModelAdapter):
    def __init__(self, model_id: str, api_key: str) -> None:
        self.model_id = model_id
        self._api_key = api_key

    async def run_agent(
        self,
        prompt: str,
        on_event: Callable[[AgentEvent], None],
        verbose: bool = False,
    ) -> ModelResponse:
        import anthropic

        client = anthropic.AsyncAnthropic(api_key=self._api_key)
        messages: list[dict] = [{"role": "user", "content": prompt}]
        proposed_writes: list[FileWrite] = []
        text_parts: list[str] = []
        start = time.monotonic()

        while True:
            response = await client.messages.create(
                model=self.model_id,
                max_tokens=8096,
                tools=_TOOLS,
                messages=messages,
            )

            tool_results = []
            for block in response.content:
                if block.type == "text":
                    text_parts.append(block.text)
                    if verbose:
                        on_event(AgentEvent("text", block.text))
                elif block.type == "tool_use":
                    if block.name == READ_TOOL_NAME:
                        path = block.input["path"]
                        on_event(AgentEvent("reading", path))
                        content = execute_read(path)
                        tool_results.append({
                            "type": "tool_result",
                            "tool_use_id": block.id,
                            "content": content,
                        })
                    elif block.name == WRITE_TOOL_NAME:
                        path = block.input["path"]
                        on_event(AgentEvent("writing", path))
                        proposed_writes.append(FileWrite(path, block.input["content"]))
                        tool_results.append({
                            "type": "tool_result",
                            "tool_use_id": block.id,
                            "content": "Write queued — will be applied if selected.",
                        })

            messages.append({"role": "assistant", "content": response.content})
            if tool_results:
                messages.append({"role": "user", "content": tool_results})

            if response.stop_reason == "end_turn":
                break

        return ModelResponse(
            model_id=self.model_id,
            text="".join(text_parts),
            latency_ms=int((time.monotonic() - start) * 1000),
            proposed_writes=proposed_writes,
        )
