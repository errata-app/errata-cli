"""Append-only preference log + simple query helpers."""

from __future__ import annotations

import hashlib
import json
import os
import uuid
from datetime import datetime, timezone
from pathlib import Path

from errata.config import settings
from errata.models.base import ModelResponse


def _prefs_path() -> Path:
    p = Path(settings.preferences_path)
    p.parent.mkdir(parents=True, exist_ok=True)
    return p


def record(
    prompt: str,
    responses: list[ModelResponse],
    selected_model: str,
    session_id: str | None = None,
) -> None:
    """Append one preference record to the JSONL log."""
    entry = {
        "ts": datetime.now(timezone.utc).isoformat(),
        "prompt_hash": "sha256:" + hashlib.sha256(prompt.encode()).hexdigest(),
        "prompt_preview": prompt[:120],
        "models": [r.model_id for r in responses],
        "selected": selected_model,
        "latencies_ms": {r.model_id: r.latency_ms for r in responses},
        "session_id": session_id or str(uuid.uuid4()),
    }
    with _prefs_path().open("a", encoding="utf-8") as f:
        f.write(json.dumps(entry) + "\n")


def load_all() -> list[dict]:
    """Return all recorded preferences as a list of dicts."""
    path = _prefs_path()
    if not path.exists():
        return []
    records = []
    with path.open("r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                records.append(json.loads(line))
    return records


def summarize() -> dict[str, int]:
    """Return a {model_id: win_count} tally."""
    tally: dict[str, int] = {}
    for entry in load_all():
        winner = entry.get("selected", "")
        tally[winner] = tally.get(winner, 0) + 1
    return tally
