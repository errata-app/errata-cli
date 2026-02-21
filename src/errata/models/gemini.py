"""Google Gemini model adapter."""

from __future__ import annotations

from collections.abc import AsyncGenerator, AsyncIterator

from errata.models.base import ModelAdapter


class GeminiAdapter(ModelAdapter):
    def __init__(self, model_id: str, api_key: str) -> None:
        self.model_id = model_id
        self._api_key = api_key

    async def stream(self, prompt: str) -> AsyncIterator[str]:
        import google.generativeai as genai

        genai.configure(api_key=self._api_key)
        model = genai.GenerativeModel(self.model_id)
        response = await model.generate_content_async(prompt, stream=True)
        async for chunk in response:
            if chunk.text:
                yield chunk.text
