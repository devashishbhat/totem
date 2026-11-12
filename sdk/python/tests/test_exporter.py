"""The exporter must never slow the agent down or raise into it."""

from __future__ import annotations

import threading

import httpx

from totem_ai._config import Config
from totem_ai._export import BATCH_SIZE, QUEUE_SIZE, Exporter, SpanRecord

from .conftest import Receiver


def config(receiver: Receiver) -> Config:
    return Config(endpoint="http://totem.test", api_key="test-key", agent_id="a", salt="s")


def record(i: int = 0) -> SpanRecord:
    return SpanRecord(
        name="totem.source",
        trace_id=b"0123456789abcdef",
        span_id=b"01234567",
        parent_span_id=b"",
        start_unix_nano=1,
        end_unix_nano=2,
        attributes={"totem.op": "source", "i": i},
    )


def test_sends_the_key_as_a_bearer_token(receiver: Receiver) -> None:
    exporter = Exporter(config(receiver), httpx.Client(transport=receiver.transport()))
    exporter.submit(record())
    assert exporter.flush(5.0)
    exporter.shutdown()
    assert receiver.headers[0]["authorization"] == "Bearer test-key"
    assert receiver.headers[0]["content-type"] == "application/x-protobuf"


def test_retries_a_server_error_then_succeeds(receiver: Receiver) -> None:
    receiver.failures_left = 2
    exporter = Exporter(config(receiver), httpx.Client(transport=receiver.transport()))
    exporter.submit(record())
    exporter.shutdown(timeout=15.0)
    assert len(receiver.requests) == 3
    assert exporter.sent == 1


def test_a_refused_key_is_not_retried(receiver: Receiver) -> None:
    receiver.status = 401
    exporter = Exporter(config(receiver), httpx.Client(transport=receiver.transport()))
    exporter.submit(record())
    exporter.shutdown(timeout=10.0)
    assert len(receiver.requests) == 1


def test_a_broken_endpoint_does_not_raise() -> None:
    def explode(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("no route to host")

    receiver = Receiver()
    exporter = Exporter(config(receiver), httpx.Client(transport=httpx.MockTransport(explode)))
    exporter.submit(record())
    exporter.shutdown(timeout=15.0)
    assert exporter.sent == 0


def test_a_full_queue_drops_the_oldest_spans(receiver: Receiver) -> None:
    # A stalled exporter must not grow without bound, and the newest events are
    # the ones worth keeping. The gate holds the exporter thread inside its
    # first request, so it can only have taken one batch.
    gate = threading.Event()

    def blocked(request: httpx.Request) -> httpx.Response:
        gate.wait(10.0)
        return httpx.Response(202, json={})

    exporter = Exporter(config(receiver), httpx.Client(transport=httpx.MockTransport(blocked)))
    try:
        for i in range(QUEUE_SIZE + BATCH_SIZE + 10):
            exporter.submit(record(i))
        assert exporter.dropped >= 10
    finally:
        gate.set()
        exporter.shutdown(timeout=10.0)
