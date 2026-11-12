"""SDK configuration."""

from __future__ import annotations

import os
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Config:
    """Everything ``totem_ai.init`` was told, resolved against the environment."""

    endpoint: str
    api_key: str
    agent_id: str
    salt: str
    side_effect_tools: frozenset[str] = frozenset()
    external_tools: frozenset[str] = frozenset()
    trusted_tools: frozenset[str] = frozenset()
    capture_payloads: bool = True
    # Redacting the recorded prompt is safe but makes replay unfaithful
    # when the value that drove the decision was one of the things
    # redacted out of it. See docs/architecture.md.
    redact_payloads: bool = True
    enabled: bool = True
    timeout_seconds: float = 5.0
    extra_headers: dict[str, str] = field(default_factory=dict)

    @property
    def traces_url(self) -> str:
        """The OTLP endpoint to POST spans to."""
        return self.endpoint.rstrip("/") + "/v1/traces"


def resolve(
    endpoint: str | None,
    api_key: str | None,
    agent_id: str | None,
    salt: str | None,
) -> tuple[str, str, str, str]:
    """Fills in anything the caller left out from the environment.

    Raises:
        ValueError: if a value is missing from both the call and the
            environment, naming the variable to set.
    """
    resolved = {
        "endpoint": endpoint or os.getenv("TM_ENDPOINT") or "http://localhost:8080",
        "api_key": api_key or os.getenv("TM_INGEST_KEY") or "",
        "agent_id": agent_id or os.getenv("TM_AGENT_ID") or "",
        "salt": salt or os.getenv("TM_PROJECT_SALT") or "",
    }
    required = {"api_key": "TM_INGEST_KEY", "agent_id": "TM_AGENT_ID", "salt": "TM_PROJECT_SALT"}
    for key, env_var in required.items():
        if not resolved[key]:
            raise ValueError(f"totem_ai.init needs {key}: pass it explicitly or set {env_var}")
    return resolved["endpoint"], resolved["api_key"], resolved["agent_id"], resolved["salt"]
