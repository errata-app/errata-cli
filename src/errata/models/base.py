"""Abstract base class for all model adapters."""

from __future__ import annotations

from abc import ABC, abstractmethod
from collections.abc import Callable
from dataclasses import dataclass, field


@dataclass
class FileWrite:
    path: str
    content: str


@dataclass
class AgentEvent:
    type: str   # "text" | "reading" | "writing" | "error"
    data: str   # text chunk, file path, or error message


@dataclass
class ModelResponse:
    model_id: str
    text: str
    latency_ms: int
    proposed_writes: list[FileWrite] = field(default_factory=list)
    error: str | None = None

    @property
    def ok(self) -> bool:
        return self.error is None


class ModelAdapter(ABC):
    """Minimal interface every provider adapter must implement."""

    model_id: str

    @abstractmethod
    async def run_agent(
        self,
        prompt: str,
        on_event: Callable[[AgentEvent], None],
        verbose: bool = False,
    ) -> ModelResponse:
        """
        Run the agentic tool-use loop for this model.

        Executes read_file tool calls immediately and intercepts write_file
        calls as proposals. Calls on_event() for each tool event and,
        if verbose=True, for each text chunk.

        Returns a ModelResponse containing the model's explanatory text
        and all proposed file writes.
        """
        ...
