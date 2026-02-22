"""Google Gemini model adapter."""

from __future__ import annotations

import time
from collections.abc import Callable

from errata.models.base import AgentEvent, FileWrite, ModelAdapter, ModelResponse
from errata.tools import READ_TOOL_NAME, TOOL_DEFINITIONS, WRITE_TOOL_NAME, execute_read


def _build_gemini_tools():
    """Translate canonical tool defs to google.generativeai protos."""
    import google.generativeai as genai

    declarations = []
    for t in TOOL_DEFINITIONS:
        props = {}
        for name, spec in t["parameters"]["properties"].items():
            props[name] = genai.protos.Schema(
                type=genai.protos.Type.STRING,
                description=spec.get("description", ""),
            )
        declarations.append(
            genai.protos.FunctionDeclaration(
                name=t["name"],
                description=t["description"],
                parameters=genai.protos.Schema(
                    type=genai.protos.Type.OBJECT,
                    properties=props,
                    required=t["parameters"].get("required", []),
                ),
            )
        )
    return [genai.protos.Tool(function_declarations=declarations)]


class GeminiAdapter(ModelAdapter):
    def __init__(self, model_id: str, api_key: str) -> None:
        self.model_id = model_id
        self._api_key = api_key

    async def run_agent(
        self,
        prompt: str,
        on_event: Callable[[AgentEvent], None],
        verbose: bool = False,
    ) -> ModelResponse:
        import google.generativeai as genai

        genai.configure(api_key=self._api_key)
        model = genai.GenerativeModel(self.model_id, tools=_build_gemini_tools())
        chat = model.start_chat()

        proposed_writes: list[FileWrite] = []
        text_parts: list[str] = []
        start = time.monotonic()

        user_message = prompt

        while True:
            response = await chat.send_message_async(user_message)
            candidate = response.candidates[0]

            function_calls = []
            for part in candidate.content.parts:
                if part.text:
                    text_parts.append(part.text)
                    if verbose:
                        on_event(AgentEvent("text", part.text))
                if part.function_call.name:
                    function_calls.append(part.function_call)

            if not function_calls:
                break

            # Build function responses to feed back
            function_responses = []
            for fc in function_calls:
                args = dict(fc.args)
                if fc.name == READ_TOOL_NAME:
                    path = args["path"]
                    on_event(AgentEvent("reading", path))
                    content = execute_read(path)
                    function_responses.append(
                        genai.protos.Part(
                            function_response=genai.protos.FunctionResponse(
                                name=fc.name,
                                response={"result": content},
                            )
                        )
                    )
                elif fc.name == WRITE_TOOL_NAME:
                    path = args["path"]
                    on_event(AgentEvent("writing", path))
                    proposed_writes.append(FileWrite(path, args["content"]))
                    function_responses.append(
                        genai.protos.Part(
                            function_response=genai.protos.FunctionResponse(
                                name=fc.name,
                                response={"result": "Write queued — will be applied if selected."},
                            )
                        )
                    )

            user_message = genai.protos.Content(parts=function_responses, role="user")

        return ModelResponse(
            model_id=self.model_id,
            text="".join(text_parts),
            latency_ms=int((time.monotonic() - start) * 1000),
            proposed_writes=proposed_writes,
        )
