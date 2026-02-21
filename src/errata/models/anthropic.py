"""Anthropic (Claude) model adapter."""

from __future__ import annotations

from collections.abc import AsyncGenerator

from errata.models.base import ModelAdapter


class AnthropicAdapter(ModelAdapter):
    def __init__(self, model_id: str, api_key: str) -> None:
        self.model_id = model_id
        self._api_key = api_key

    async def stream(self, prompt: str) -> AsyncGenerator[str, None]:
        import anthropic

        client = anthropic.AsyncAnthropic(api_key=self._api_key)
        async with client.messages.stream(
            model=self.model_id,
            max_tokens=8096,
            messages=[{"role": "user", "content": prompt}],
        ) as stream:
            async for text in stream.text_stream:
                yield text
