"""Totem: trace an agent's actions back through its memory to their source.

Typical use with LangGraph::

    import totem_ai as tm

    tm.init(agent_id="crm-assistant", side_effect_tools={"send_email"})
    store = tm.wrap_store(InMemoryStore())
    handler = tm.CallbackHandler()
    with tm.run(scope="user:42"):
        graph.invoke(inputs, config={"callbacks": [handler]})
    tm.shutdown()

Everything Totem records is redacted before it leaves the process, and nothing
here raises into the agent: if Totem cannot record something it logs at debug
level and the agent carries on.
"""

from __future__ import annotations

import atexit
import logging
from collections.abc import Iterable, Iterator
from contextlib import contextmanager
from typing import TYPE_CHECKING, Any

from . import _core, _events
from ._config import Config, resolve
from ._context import RunContext, current, new_run
from ._export import Exporter
from ._recorder import Recorder

if TYPE_CHECKING:
    from ._callbacks import CallbackHandler as CallbackHandler

__all__ = [
    "CallbackHandler",
    "Config",
    "canonical_json",
    "current_run",
    "fingerprint",
    "init",
    "record_decision",
    "record_llm_call",
    "record_memory_read",
    "record_memory_write",
    "record_source",
    "redact",
    "run",
    "shutdown",
    "wrap_store",
]

log = logging.getLogger("totem_ai")

_recorder: Recorder | None = None


def init(
    *,
    endpoint: str | None = None,
    api_key: str | None = None,
    agent_id: str | None = None,
    salt: str | None = None,
    side_effect_tools: Iterable[str] = (),
    external_tools: Iterable[str] = (),
    trusted_tools: Iterable[str] = (),
    capture_payloads: bool = True,
    redact_payloads: bool = True,
    enabled: bool = True,
    timeout_seconds: float = 5.0,
    http_client: Any | None = None,
) -> Recorder:
    """Configures Totem and starts the exporter.

    Args:
        endpoint: Base URL of totem-server. Defaults to ``TM_ENDPOINT`` or
            ``http://localhost:8080``.
        api_key: Ingest key; defaults to ``TM_INGEST_KEY``.
        agent_id: Name of the agent being traced; defaults to ``TM_AGENT_ID``.
        salt: Project salt for fingerprints; defaults to ``TM_PROJECT_SALT``.
            It must match the server's ``TM_PROJECT_SALT``.
        side_effect_tools: Tools that change the world. A decision calling one
            of these is what an alert is about.
        external_tools: Tools whose output is an external document, and so
            untrusted, rather than ordinary tool output.
        trusted_tools: Tools whose output may be trusted one tier higher.
        capture_payloads: Record the full model request, which is what makes
            replay possible. Turn it off to record less.
        redact_payloads: Redact the recorded request like everything else.
            Leave it on unless you need replay to be faithful: a redacted
            prompt no longer contains the value the agent acted on, so the
            model cannot reproduce the decision and replay refuses to grade
            it. Turning it off stores prompts as the agent sent them, and
            needs ``TM_REDACT_PAYLOADS=false`` on the server too.
        enabled: Set ``False`` to make every call a no-op.
        timeout_seconds: HTTP timeout for an export request.
        http_client: An ``httpx.Client`` to export through, instead of one of
            Totem's own. The SDK's own tests use it to capture spans.

    Returns:
        The recorder, which the explicit API uses. Keeping it is optional.
    """
    global _recorder
    resolved_endpoint, resolved_key, resolved_agent, resolved_salt = resolve(
        endpoint, api_key, agent_id, salt
    )
    config = Config(
        endpoint=resolved_endpoint,
        api_key=resolved_key,
        agent_id=resolved_agent,
        salt=resolved_salt,
        side_effect_tools=frozenset(side_effect_tools),
        external_tools=frozenset(external_tools),
        trusted_tools=frozenset(trusted_tools),
        capture_payloads=capture_payloads,
        redact_payloads=redact_payloads,
        enabled=enabled,
        timeout_seconds=timeout_seconds,
    )
    if _recorder is not None:
        _recorder.exporter.shutdown()
    _recorder = Recorder(config, Exporter(config, http_client))
    return _recorder


def shutdown(timeout: float = 10.0) -> None:
    """Flushes queued spans and stops the exporter thread."""
    global _recorder
    if _recorder is None:
        return
    _recorder.exporter.shutdown(timeout)
    _recorder = None


@atexit.register
def _flush_at_exit() -> None:
    if _recorder is not None:
        try:
            _recorder.exporter.shutdown(5.0)
        except Exception:
            log.debug("totem: flush at exit failed", exc_info=True)


def recorder() -> Recorder | None:
    """The configured recorder, or ``None`` when :func:`init` has not run."""
    return _recorder


@contextmanager
def run(scope: str = "", run_id: str | None = None) -> Iterator[RunContext]:
    """Marks one agent run.

    Args:
        scope: Who or what the run is for, for example ``"user:42"``. It is
            recorded as-is and shown on the run page.
        run_id: Use an id of your own instead of a generated one.
    """
    from ._context import activate, deactivate

    ctx = new_run(run_id, scope)
    token = activate(ctx)
    try:
        if _recorder is not None:
            _recorder.record_run(ctx)
        yield ctx
    finally:
        deactivate(token)


def current_run() -> RunContext | None:
    """The run in scope, or ``None``."""
    return current()


# -- the explicit API -----------------------------------------------------
#
# These are for agents that are not built on LangGraph, and for recording
# something the wrappers cannot see.


def record_source(
    kind: _events.SourceKind,
    text: str,
    origin: str = "",
    trust: int | None = None,
) -> str | None:
    """Records information entering the agent. Returns its source id."""
    return _recorder.record_source(kind, text, origin, trust) if _recorder else None


def record_memory_write(
    memory_id: str,
    text: str,
    *,
    deleted: bool = False,
    source_ids: list[str] | None = None,
    derived_from: list[str] | None = None,
) -> None:
    """Records a memory write, with the lineage of its content."""
    if _recorder:
        _recorder.record_memory_write(
            memory_id, text, deleted=deleted, source_ids=source_ids, derived_from=derived_from
        )


def record_memory_read(results: list[tuple[str, str, float]]) -> None:
    """Records a memory read. Each result is ``(memory_id, text, rank)``."""
    if _recorder:
        _recorder.record_memory_read(results)


def record_llm_call(
    messages: list[str],
    *,
    model: str = "",
    payload: dict[str, Any] | None = None,
) -> str | None:
    """Records a model call and what of the agent's memory reached it."""
    return _recorder.record_llm_call(messages, model=model, payload=payload) if _recorder else None


def record_decision(
    tool: str,
    args: dict[str, Any] | None = None,
    *,
    side_effect: bool | None = None,
    call_id: str | None = None,
) -> str | None:
    """Records a tool call or a final answer, with its value flow."""
    if not _recorder:
        return None
    return _recorder.record_decision(tool, args, side_effect=side_effect, call_id=call_id)


def wrap_store(store: Any) -> Any:
    """Wraps a LangGraph ``BaseStore`` so every read and write is recorded."""
    from ._store import wrap_store as _wrap

    return _wrap(store)


def redact(text: str) -> str:
    """The redacted form of some text, as Totem would store it."""
    return _recorder.redact(text) if _recorder else text


def fingerprint(text: str) -> str:
    """The identity Totem knows a piece of text by."""
    return _recorder.fingerprint(text) if _recorder else ""


def canonical_json(value: str) -> str:
    """Canonical JSON (RFC 8785) for a JSON string."""
    return _core.canonical_json(value)


def __getattr__(name: str) -> Any:
    # CallbackHandler needs langchain-core, which the explicit API does not,
    # so it is imported on first use rather than at import time.
    if name == "CallbackHandler":
        from ._callbacks import CallbackHandler

        return CallbackHandler
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
