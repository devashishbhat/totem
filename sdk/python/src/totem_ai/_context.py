"""The run context: what the agent has seen so far in this run.

The context lives in a :mod:`contextvars` variable, so concurrent runs in the
same process (threads, asyncio tasks) keep their own view without the caller
passing anything around.
"""

from __future__ import annotations

import os
import threading
import uuid
from contextvars import ContextVar
from dataclasses import dataclass, field


@dataclass
class SeenSource:
    """A piece of information that entered the agent during this run."""

    source_id: str
    kind: str
    origin: str
    text: str
    fingerprint: str


@dataclass
class SeenMemory:
    """A memory the agent read or wrote during this run."""

    memory_id: str
    text: str
    fingerprint: str


@dataclass
class Step:
    """The model call currently in flight, and what reached its prompt.

    A memory written while a step is active is attributed to that step's
    references, which is the ``step`` lineage precision: it names the handful
    of things the model had actually read, rather than everything the run has
    seen.
    """

    call_id: str
    source_ids: list[str] = field(default_factory=list)
    memory_ids: list[str] = field(default_factory=list)
    tool: str | None = None


@dataclass
class RunContext:
    """One agent run."""

    run_id: str
    scope: str
    trace_id: bytes
    root_span_id: bytes
    sources: list[SeenSource] = field(default_factory=list)
    memories: dict[str, SeenMemory] = field(default_factory=dict)
    step: Step | None = None
    _lock: threading.Lock = field(default_factory=threading.Lock, repr=False)

    def add_source(self, source: SeenSource) -> tuple[SeenSource, bool]:
        """Records a source.

        Text the run has already seen is not recorded twice. Returns the
        source that is now in the context and whether it is new, so the caller
        knows whether to emit an event for it.
        """
        with self._lock:
            for existing in self.sources:
                if existing.fingerprint == source.fingerprint:
                    return existing, False
            self.sources.append(source)
            return source, True

    def add_memory(self, memory: SeenMemory) -> None:
        """Records a memory the agent read or wrote."""
        with self._lock:
            self.memories[memory.memory_id] = memory

    def candidates(self) -> list[tuple[str, str]]:
        """Everything that could have influenced the next decision.

        Each entry is ``(ref, text)`` where ``ref`` is ``"memory:<key>"`` or
        ``"source:<uuid>"``, so a match can be turned back into a typed
        reference.
        """
        with self._lock:
            out = [(f"memory:{m.memory_id}", m.text) for m in self.memories.values() if m.text]
            out += [(f"source:{s.source_id}", s.text) for s in self.sources if s.text]
            return out

    def source_ids(self) -> list[str]:
        """Every source seen in this run, for run-precision lineage."""
        with self._lock:
            return [s.source_id for s in self.sources]


_current: ContextVar[RunContext | None] = ContextVar("totem_run", default=None)


def current() -> RunContext | None:
    """The run context in scope, or ``None`` outside a run."""
    return _current.get()


def new_run(run_id: str | None, scope: str) -> RunContext:
    """Builds a run context. Caller is responsible for activating it."""
    return RunContext(
        run_id=run_id or f"run-{uuid.uuid4()}",
        scope=scope,
        trace_id=os.urandom(16),
        root_span_id=os.urandom(8),
    )


def activate(ctx: RunContext | None):
    """Makes ``ctx`` the current run and returns the reset token."""
    return _current.set(ctx)


def deactivate(token) -> None:
    """Restores whatever run was current before :func:`activate`."""
    _current.reset(token)


def split_ref(ref: str) -> tuple[str, str]:
    """Turns ``"memory:k"`` back into ``("memory", "k")``."""
    kind, _, rest = ref.partition(":")
    return kind, rest
