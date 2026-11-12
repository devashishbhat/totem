"""The recorder: turns what the agent did into events.

Everything that leaves the process is redacted first, and the only identity
that travels is the fingerprint of the redacted text. Raw text stays in the
run context, where the matchers need it.
"""

from __future__ import annotations

import json
import logging
import time
import uuid
from typing import Any

from . import _core
from . import _events as ev
from ._config import Config
from ._context import RunContext, SeenMemory, SeenSource, Step, current, split_ref
from ._export import Exporter, SpanRecord

log = logging.getLogger("totem_ai")


class Recorder:
    """Emits Totem events for one configured project."""

    def __init__(self, config: Config, exporter: Exporter) -> None:
        self.config = config
        self.exporter = exporter

    # -- primitives ------------------------------------------------------

    def redact(self, text: str) -> str:
        """The form of the text that is safe to store."""
        redacted, _ = _core.redact(self.config.salt, text)
        return redacted

    def fingerprint(self, text: str) -> str:
        """The identity the server will know this text by."""
        return _core.fingerprint(self.config.salt, text)

    def emit(self, op: ev.Op, attrs: dict[str, Any], started_ns: int) -> None:
        """Queues one event as an OTLP span."""
        ctx = current()
        if ctx is None or not self.config.enabled:
            return
        now = time.time_ns()
        attrs = {
            ev.OP: op,
            ev.EVENT_ID: str(uuid.uuid4()),
            ev.AGENT_ID: self.config.agent_id,
            ev.RUN_ID: ctx.run_id,
            ev.OVERHEAD_US: max(0, (time.perf_counter_ns() - started_ns) // 1000),
            **attrs,
        }
        self.exporter.submit(
            SpanRecord(
                name=f"totem.{op}",
                trace_id=ctx.trace_id,
                span_id=_span_id(),
                parent_span_id=b"" if op == "run" else ctx.root_span_id,
                start_unix_nano=now,
                end_unix_nano=now,
                attributes=attrs,
            )
        )

    # -- the six ops -----------------------------------------------------

    def record_run(self, ctx: RunContext) -> None:
        """The root event of a run."""
        started = time.perf_counter_ns()
        now = time.time_ns()
        attrs = {
            ev.OP: "run",
            ev.EVENT_ID: str(uuid.uuid4()),
            ev.AGENT_ID: self.config.agent_id,
            ev.RUN_ID: ctx.run_id,
            ev.RUN_SCOPE: ctx.scope,
            ev.OVERHEAD_US: max(0, (time.perf_counter_ns() - started) // 1000),
        }
        self.exporter.submit(
            SpanRecord(
                name="totem.run",
                trace_id=ctx.trace_id,
                span_id=ctx.root_span_id,
                parent_span_id=b"",
                start_unix_nano=now,
                end_unix_nano=now,
                attributes=attrs,
            )
        )

    def record_source(
        self,
        kind: ev.SourceKind,
        text: str,
        origin: str = "",
        trust: int | None = None,
    ) -> str | None:
        """Records information entering the agent. Returns its source id."""
        started = time.perf_counter_ns()
        ctx = current()
        if ctx is None:
            return None
        fingerprint = self.fingerprint(text)
        source, is_new = ctx.add_source(
            SeenSource(
                source_id=str(uuid.uuid4()),
                kind=kind,
                origin=origin,
                text=text,
                fingerprint=fingerprint,
            )
        )
        if is_new:
            self.emit(
                "source",
                {
                    ev.SOURCE_ID: source.source_id,
                    ev.SOURCE_KIND: kind,
                    ev.SOURCE_ORIGIN: origin,
                    ev.SOURCE_TRUST: trust,
                    ev.HMAC: fingerprint,
                    ev.CONTENT: self.redact(text),
                },
                started,
            )
        return source.source_id

    def record_memory_write(
        self,
        memory_id: str,
        text: str,
        *,
        deleted: bool = False,
        source_ids: list[str] | None = None,
        derived_from: list[str] | None = None,
    ) -> None:
        """Records a store put or delete, with the lineage of its content."""
        started = time.perf_counter_ns()
        ctx = current()
        if ctx is None:
            return
        fingerprint = self.fingerprint(text)
        ctx.add_memory(SeenMemory(memory_id=memory_id, text=text, fingerprint=fingerprint))

        if source_ids is None and derived_from is None:
            source_ids, derived_from, precision = self._lineage(ctx)
        else:
            precision = "step"
        self.emit(
            "memory.write",
            {
                ev.MEMORY_ID: memory_id,
                ev.HMAC: fingerprint,
                ev.CONTENT: self.redact(text),
                ev.MEMORY_DELETED: deleted,
                ev.SOURCE_IDS: source_ids or [],
                ev.MEMORY_DERIVED_FROM: json.dumps(derived_from or []),
                ev.LINEAGE_PRECISION: precision,
            },
            started,
        )

    def record_memory_read(self, results: list[tuple[str, str, float]]) -> None:
        """Records a store get or search. Each result is ``(id, text, rank)``."""
        started = time.perf_counter_ns()
        ctx = current()
        if ctx is None:
            return
        payload = []
        for memory_id, text, rank in results:
            fingerprint = self.fingerprint(text)
            ctx.add_memory(SeenMemory(memory_id=memory_id, text=text, fingerprint=fingerprint))
            payload.append({"memory_id": memory_id, "hmac": fingerprint, "rank": rank})
        self.emit("memory.read", {ev.READ_RESULTS: json.dumps(payload)}, started)

    def record_llm_call(
        self,
        messages: list[str],
        *,
        model: str = "",
        payload: dict[str, Any] | None = None,
        call_id: str | None = None,
    ) -> str | None:
        """Records one model call and what of the agent's memory reached it.

        Returns the call id, which the decisions that follow refer to.
        """
        started = time.perf_counter_ns()
        ctx = current()
        if ctx is None:
            return None
        call_id = call_id or str(uuid.uuid4())
        candidates = ctx.candidates()
        in_context: list[dict[str, Any]] = []
        step = Step(call_id=call_id)
        try:
            matches = _core.match_in_context(candidates, messages)
        except Exception:
            log.debug("totem: in-context matching failed", exc_info=True)
            matches = []
        for match in matches:
            ref_type, ref_id = split_ref(match["id"])
            fingerprint = self._fingerprint_of(ctx, ref_type, ref_id)
            in_context.append(
                {
                    "ref_type": ref_type,
                    "ref_id": ref_id,
                    "hmac": fingerprint,
                    "match": match["match"],
                    "spans": [list(span) for span in match["spans"]],
                }
            )
            if ref_type == "memory":
                step.memory_ids.append(ref_id)
            else:
                step.source_ids.append(ref_id)
        ctx.step = step

        attrs: dict[str, Any] = {
            ev.LLM_CALL_ID: call_id,
            ev.LLM_MODEL: model,
            ev.LLM_IN_CONTEXT: json.dumps(in_context),
        }
        if self.config.capture_payloads and payload is not None:
            raw = json.dumps(payload)
            attrs[ev.LLM_PAYLOAD] = self.redact(raw) if self.config.redact_payloads else raw
        self.emit("llm.call", attrs, started)
        return call_id

    def record_decision(
        self,
        tool: str,
        args: dict[str, Any] | None = None,
        *,
        side_effect: bool | None = None,
        call_id: str | None = None,
        decision_id: str | None = None,
    ) -> str | None:
        """Records a tool call or a final answer, with its value flow."""
        started = time.perf_counter_ns()
        ctx = current()
        if ctx is None:
            return None
        decision_id = decision_id or str(uuid.uuid4())
        args_json = json.dumps(args or {}, default=str)
        if side_effect is None:
            side_effect = tool in self.config.side_effect_tools
        if call_id is None and ctx.step is not None:
            call_id = ctx.step.call_id

        value_flow: list[dict[str, Any]] = []
        try:
            flows = _core.match_value_flow(args_json, ctx.candidates())
        except Exception:
            log.debug("totem: value-flow matching failed", exc_info=True)
            flows = []
        for flow in flows:
            ref_type, ref_id = split_ref(flow["id"])
            value_flow.append(
                {
                    "ref_type": ref_type,
                    "ref_id": ref_id,
                    "hmac": self._fingerprint_of(ctx, ref_type, ref_id),
                    "arg_path": flow["arg_path"],
                    "match": flow["match"],
                }
            )
        self.emit(
            "decision",
            {
                ev.DECISION_ID: decision_id,
                ev.DECISION_TOOL: tool,
                ev.DECISION_SIDE_EFFECT: side_effect,
                ev.DECISION_ARGS: self.redact(args_json),
                ev.DECISION_VALUE_FLOW: json.dumps(value_flow),
                ev.LLM_CALL_ID: call_id,
            },
            started,
        )
        return decision_id

    # -- helpers ---------------------------------------------------------

    def _lineage(self, ctx: RunContext) -> tuple[list[str], list[str], str]:
        """Where a memory written right now came from.

        While a model call is in flight, the answer is what that call had in
        its prompt, which is the ``step`` precision. Outside one, the honest
        answer is only "something this run saw", which is ``run``.
        """
        if ctx.step is not None and (ctx.step.source_ids or ctx.step.memory_ids):
            return list(ctx.step.source_ids), list(ctx.step.memory_ids), "step"
        return ctx.source_ids(), [], "run"

    def _fingerprint_of(self, ctx: RunContext, ref_type: str, ref_id: str) -> str:
        if ref_type == "memory":
            memory = ctx.memories.get(ref_id)
            return memory.fingerprint if memory else ""
        for source in ctx.sources:
            if source.source_id == ref_id:
                return source.fingerprint
        return ""


def _span_id() -> bytes:
    return uuid.uuid4().bytes[:8]
