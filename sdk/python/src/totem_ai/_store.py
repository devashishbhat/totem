"""The LangGraph store wrapper.

A store is where an agent's memory lives, so wrapping ``batch`` and ``abatch``
is enough to see every read and write: the concrete ``get``, ``put``,
``search`` and ``delete`` helpers on ``BaseStore`` all go through them.
"""

from __future__ import annotations

import json
import logging
from collections.abc import Iterable, Iterator
from typing import Any

from langgraph.store.base import BaseStore, GetOp, Op, PutOp, SearchOp

from . import _core

log = logging.getLogger("totem_ai")


def memory_key(namespace: tuple[str, ...], key: str) -> str:
    """The id Totem knows a memory by: its namespace and key, slash-joined."""
    return "/".join([*namespace, key])


def value_text(value: Any) -> str:
    """The text of a stored value, as it would appear in a prompt.

    A stored value is usually a dict wrapping one string, and that string is
    what reaches the model, so it is the text Totem matches on. A value with
    several strings contributes each of them, one per line; anything else falls
    back to canonical JSON, which at least fingerprints consistently.
    """
    if isinstance(value, str):
        return value
    strings = list(_string_leaves(value))
    if strings:
        return "\n".join(strings)
    try:
        return _core.canonical_json(json.dumps(value, default=str))
    except Exception:
        return str(value)


def _string_leaves(value: Any) -> Iterator[str]:
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from _string_leaves(item)
    elif isinstance(value, (list, tuple)):
        for item in value:
            yield from _string_leaves(item)


def wrap_store(store: BaseStore) -> BaseStore:
    """Returns a store that records every read and write it performs."""
    return TracedStore(store)


class TracedStore(BaseStore):
    """A ``BaseStore`` that reports what passes through it to Totem."""

    def __init__(self, inner: BaseStore) -> None:
        object.__setattr__(self, "_inner", inner)

    @property
    def inner(self) -> BaseStore:
        """The store being wrapped."""
        return self._inner

    def batch(self, ops: Iterable[Op]) -> list[Any]:
        ops = list(ops)
        results = self._inner.batch(ops)
        self._record(ops, results)
        return results

    async def abatch(self, ops: Iterable[Op]) -> list[Any]:
        ops = list(ops)
        results = await self._inner.abatch(ops)
        self._record(ops, results)
        return results

    def __getattr__(self, name: str) -> Any:
        # Anything Totem does not wrap is the inner store's business.
        if name == "_inner":
            raise AttributeError(name)
        return getattr(self._inner, name)

    def _record(self, ops: list[Op], results: list[Any]) -> None:
        from . import recorder

        rec = recorder()
        if rec is None:
            return
        try:
            reads: list[tuple[str, str, float]] = []
            for op, result in zip(ops, results, strict=False):
                if isinstance(op, PutOp):
                    key = memory_key(op.namespace, op.key)
                    if op.value is None:
                        rec.record_memory_write(key, "", deleted=True)
                    else:
                        rec.record_memory_write(key, value_text(op.value))
                elif isinstance(op, GetOp):
                    if result is not None:
                        reads.append(
                            (memory_key(op.namespace, op.key), value_text(result.value), 1.0)
                        )
                elif isinstance(op, SearchOp):
                    for item in result or []:
                        reads.append(
                            (
                                memory_key(item.namespace, item.key),
                                value_text(item.value),
                                float(getattr(item, "score", None) or 1.0),
                            )
                        )
            if reads:
                rec.record_memory_read(reads)
        except Exception:
            log.debug("totem: could not record a store operation", exc_info=True)
