"""The OTLP exporter.

Instrumentation must never slow the agent down or take it with it when
something goes wrong, so every span is handed to a bounded queue and written by
a background thread, and every failure in here is logged and swallowed.
"""

from __future__ import annotations

import logging
import threading
import time
from collections import deque
from dataclasses import dataclass, field
from typing import Any

import httpx
from opentelemetry.proto.collector.trace.v1.trace_service_pb2 import ExportTraceServiceRequest
from opentelemetry.proto.common.v1.common_pb2 import AnyValue, ArrayValue, KeyValue
from opentelemetry.proto.trace.v1.trace_pb2 import ResourceSpans, ScopeSpans, Span

from ._config import Config

log = logging.getLogger("totem_ai")

#: How many spans may wait to be sent. When the queue is full the oldest span
#: is dropped: recent events describe what the agent is doing now.
QUEUE_SIZE = 5_000
#: Spans per export request.
BATCH_SIZE = 256
#: How long a partial batch waits before it is sent anyway.
FLUSH_INTERVAL = 1.0
#: Attempts per export request.
MAX_ATTEMPTS = 3


@dataclass
class SpanRecord:
    """One event, ready to be encoded as an OTLP span."""

    name: str
    trace_id: bytes
    span_id: bytes
    parent_span_id: bytes
    start_unix_nano: int
    end_unix_nano: int
    attributes: dict[str, Any] = field(default_factory=dict)


class Exporter:
    """Sends spans to ``POST /v1/traces``."""

    def __init__(self, config: Config, client: httpx.Client | None = None) -> None:
        self._config = config
        self._client = client or httpx.Client(timeout=config.timeout_seconds)
        self._owns_client = client is None
        self._queue: deque[SpanRecord] = deque(maxlen=QUEUE_SIZE)
        self._cond = threading.Condition()
        self._stopping = False
        self._dropped = 0
        self._sent = 0
        self._idle = threading.Event()
        self._idle.set()
        self._thread = threading.Thread(target=self._loop, name="totem-exporter", daemon=True)
        self._thread.start()

    @property
    def dropped(self) -> int:
        """How many spans were dropped because the queue was full."""
        return self._dropped

    @property
    def sent(self) -> int:
        """How many spans have been accepted by the server."""
        return self._sent

    def submit(self, record: SpanRecord) -> None:
        """Queues one span. Never raises and never blocks on the network."""
        try:
            with self._cond:
                if self._stopping:
                    return
                if len(self._queue) == QUEUE_SIZE:
                    self._dropped += 1
                self._queue.append(record)
                self._idle.clear()
                self._cond.notify()
        except Exception:
            log.debug("totem: could not queue a span", exc_info=True)

    def flush(self, timeout: float = 10.0) -> bool:
        """Waits until the queue has drained. Returns whether it emptied."""
        with self._cond:
            self._cond.notify_all()
        return self._idle.wait(timeout)

    def shutdown(self, timeout: float = 10.0) -> None:
        """Drains the queue and stops the thread."""
        self.flush(timeout)
        with self._cond:
            self._stopping = True
            self._cond.notify_all()
        self._thread.join(timeout)
        if self._owns_client:
            self._client.close()

    def _loop(self) -> None:
        while True:
            batch = self._take_batch()
            if batch:
                self._export(batch)
                with self._cond:
                    if not self._queue:
                        self._idle.set()
                continue
            with self._cond:
                if self._stopping:
                    self._idle.set()
                    return
                self._idle.set()

    def _take_batch(self) -> list[SpanRecord]:
        with self._cond:
            deadline = time.monotonic() + FLUSH_INTERVAL
            while len(self._queue) < BATCH_SIZE and not self._stopping:
                remaining = deadline - time.monotonic()
                if remaining <= 0 or self._queue:
                    break
                self._cond.wait(remaining)
            size = min(len(self._queue), BATCH_SIZE)
            return [self._queue.popleft() for _ in range(size)]

    def _export(self, batch: list[SpanRecord]) -> None:
        try:
            body = _encode(batch).SerializeToString()
        except Exception:
            log.debug("totem: could not encode %d spans", len(batch), exc_info=True)
            return
        headers = {
            "Content-Type": "application/x-protobuf",
            "Authorization": f"Bearer {self._config.api_key}",
            **self._config.extra_headers,
        }
        backoff = 0.2
        for attempt in range(1, MAX_ATTEMPTS + 1):
            try:
                response = self._client.post(self._config.traces_url, content=body, headers=headers)
                if response.status_code < 300:
                    self._sent += len(batch)
                    return
                # A rejected key or a malformed body will not fix itself.
                if response.status_code in (400, 401, 403, 413):
                    log.debug(
                        "totem: server refused %d spans with %s: %s",
                        len(batch),
                        response.status_code,
                        response.text[:200],
                    )
                    return
                log.debug("totem: export attempt %d got %s", attempt, response.status_code)
            except Exception:
                log.debug("totem: export attempt %d failed", attempt, exc_info=True)
            if attempt < MAX_ATTEMPTS:
                time.sleep(backoff)
                backoff *= 2.5
        log.debug("totem: giving up on %d spans", len(batch))


def _encode(batch: list[SpanRecord]) -> ExportTraceServiceRequest:
    spans = [
        Span(
            trace_id=r.trace_id,
            span_id=r.span_id,
            parent_span_id=r.parent_span_id,
            name=r.name,
            kind=Span.SPAN_KIND_INTERNAL,
            start_time_unix_nano=r.start_unix_nano,
            end_time_unix_nano=r.end_unix_nano,
            attributes=[_kv(k, v) for k, v in r.attributes.items() if v is not None],
        )
        for r in batch
    ]
    return ExportTraceServiceRequest(
        resource_spans=[ResourceSpans(scope_spans=[ScopeSpans(spans=spans)])]
    )


def _kv(key: str, value: Any) -> KeyValue:
    return KeyValue(key=key, value=_any_value(value))


def _any_value(value: Any) -> AnyValue:
    if isinstance(value, bool):
        return AnyValue(bool_value=value)
    if isinstance(value, int):
        return AnyValue(int_value=value)
    if isinstance(value, float):
        return AnyValue(double_value=value)
    if isinstance(value, (list, tuple)):
        return AnyValue(array_value=ArrayValue(values=[_any_value(v) for v in value]))
    return AnyValue(string_value=str(value))
