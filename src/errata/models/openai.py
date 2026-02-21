"""OpenAI model adapter."""

from __future__ import annotations

from collections.abc import AsyncGenerator, AsyncIterator

from errata.models.base import ModelAdapter


class OpenAIAdapter(ModelAdapter):
    def __init__(self, model_id: str, api_key: str) -> None:
        self.model_id = model_id
        self._api_key = api_key

    async def stream(self, prompt: str) -> AsyncIterator[str]:
        from openai import AsyncOpenAI

        client = AsyncOpenAI(api_key=self._api_key)
        stream = await client.chat.completions.create(
            model=self.model_id,
            messages=[{"role": "user", "content": prompt}],
            stream=True,
        )
        async for chunk in stream:
            delta = chunk.choices[0].delta.content
            if delta:
                yield delta
