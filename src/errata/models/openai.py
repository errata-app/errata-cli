"""OpenAI model adapter."""

from __future__ import annotations

import json
import time
from collections.abc import Callable

from errata.models.base import AgentEvent, FileWrite, ModelAdapter, ModelResponse
from errata.tools import READ_TOOL_NAME, TOOL_DEFINITIONS, WRITE_TOOL_NAME, execute_read

# Translate canonical tool defs to OpenAI's function-calling format
_TOOLS = [
    {
        "type": "function",
        "function": {
            "name": t["name"],
            "description": t["description"],
            "parameters": t["parameters"],
        },
    }
    for t in TOOL_DEFINITIONS
]


class OpenAIAdapter(ModelAdapter):
    def __init__(self, model_id: str, api_key: str) -> None:
        self.model_id = model_id
        self._api_key = api_key

    async def run_agent(
        self,
        prompt: str,
        on_event: Callable[[AgentEvent], None],
        verbose: bool = False,
    ) -> ModelResponse:
        from openai import AsyncOpenAI

        client = AsyncOpenAI(api_key=self._api_key)
        messages: list[dict] = [{"role": "user", "content": prompt}]
        proposed_writes: list[FileWrite] = []
        text_parts: list[str] = []
        start = time.monotonic()

        while True:
            response = await client.chat.completions.create(
                model=self.model_id,
                tools=_TOOLS,
                messages=messages,
            )

            message = response.choices[0].message
            messages.append(message.model_dump(exclude_unset=False))

            if message.content:
                text_parts.append(message.content)
                if verbose:
                    on_event(AgentEvent("text", message.content))

            if not message.tool_calls:
                break

            tool_results = []
            for tc in message.tool_calls:
                args = json.loads(tc.function.arguments)
                if tc.function.name == READ_TOOL_NAME:
                    path = args["path"]
                    on_event(AgentEvent("reading", path))
                    content = execute_read(path)
                    tool_results.append({
                        "role": "tool",
                        "tool_call_id": tc.id,
                        "content": content,
                    })
                elif tc.function.name == WRITE_TOOL_NAME:
                    path = args["path"]
                    on_event(AgentEvent("writing", path))
                    proposed_writes.append(FileWrite(path, args["content"]))
                    tool_results.append({
                        "role": "tool",
                        "tool_call_id": tc.id,
                        "content": "Write queued — will be applied if selected.",
                    })

            messages.extend(tool_results)

            if response.choices[0].finish_reason == "stop":
                break

        return ModelResponse(
            model_id=self.model_id,
            text="".join(text_parts),
            latency_ms=int((time.monotonic() - start) * 1000),
            proposed_writes=proposed_writes,
        )
