"""The event contract: the op names and attribute keys the server validates.

These strings are the wire format, so they are written down once here and
mirrored in ``server/internal/event``.
"""

from __future__ import annotations

from typing import Final, Literal

Op = Literal["run", "source", "memory.write", "memory.read", "llm.call", "decision"]

SourceKind = Literal["user_message", "system_config", "tool_output", "external_document", "unknown"]

OP: Final = "totem.op"
EVENT_ID: Final = "totem.event.id"
AGENT_ID: Final = "totem.agent.id"
RUN_ID: Final = "totem.run.id"
RUN_SCOPE: Final = "totem.run.scope"
OVERHEAD_US: Final = "totem.sdk.overhead_us"

SOURCE_ID: Final = "totem.source.id"
SOURCE_KIND: Final = "totem.source.kind"
SOURCE_ORIGIN: Final = "totem.source.origin"
SOURCE_TRUST: Final = "totem.source.trust"
SOURCE_IDS: Final = "totem.source.ids"

HMAC: Final = "totem.hmac"
CONTENT: Final = "totem.content"

MEMORY_ID: Final = "totem.memory.id"
MEMORY_DELETED: Final = "totem.memory.deleted"
MEMORY_DERIVED_FROM: Final = "totem.memory.derived_from"
LINEAGE_PRECISION: Final = "totem.lineage.precision"

READ_RESULTS: Final = "totem.read.results"

LLM_CALL_ID: Final = "totem.llm.call_id"
LLM_MODEL: Final = "gen_ai.request.model"
LLM_IN_CONTEXT: Final = "totem.llm.in_context"
LLM_PAYLOAD: Final = "totem.llm.payload"

DECISION_ID: Final = "totem.decision.id"
DECISION_TOOL: Final = "totem.decision.tool"
DECISION_SIDE_EFFECT: Final = "totem.decision.side_effect"
DECISION_ARGS: Final = "totem.decision.args"
DECISION_VALUE_FLOW: Final = "totem.decision.value_flow"
