"""Test fixtures: a fake ingest endpoint that decodes what the SDK sent."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

import httpx
import pytest
from opentelemetry.proto.collector.trace.v1.trace_service_pb2 import ExportTraceServiceRequest

import totem_ai as tm

SALT = "test-salt"


@dataclass
class Receiver:
    """Collects the spans the exporter sends and decodes their attributes."""

    status: int = 202
    requests: list[dict[str, Any]] = field(default_factory=list)
    spans: list[dict[str, Any]] = field(default_factory=list)
    headers: list[httpx.Headers] = field(default_factory=list)
    failures_left: int = 0

    def transport(self) -> httpx.MockTransport:
        return httpx.MockTransport(self._handle)

    def _handle(self, request: httpx.Request) -> httpx.Response:
        self.headers.append(request.headers)
        decoded = ExportTraceServiceRequest()
        decoded.ParseFromString(request.content)
        batch = []
        for resource in decoded.resource_spans:
            for scope in resource.scope_spans:
                for span in scope.spans:
                    batch.append(
                        {
                            "name": span.name,
                            "trace_id": bytes(span.trace_id),
                            "span_id": bytes(span.span_id),
                            "parent_span_id": bytes(span.parent_span_id),
                            **{kv.key: _value(kv.value) for kv in span.attributes},
                        }
                    )
        self.requests.append({"spans": batch})
        if self.failures_left > 0:
            self.failures_left -= 1
            return httpx.Response(503, text="try again")
        self.spans.extend(batch)
        return httpx.Response(self.status, json={"accepted": len(batch)})

    def ops(self, op: str) -> list[dict[str, Any]]:
        """Every span of one op, in the order they were sent."""
        return [s for s in self.spans if s.get("totem.op") == op]

    def one(self, op: str) -> dict[str, Any]:
        """The only span of one op."""
        found = self.ops(op)
        assert len(found) == 1, f"expected one {op} span, got {len(found)}: {self.spans}"
        return found[0]


def _value(value: Any) -> Any:
    which = value.WhichOneof("value")
    if which == "array_value":
        return [_value(v) for v in value.array_value.values]
    return getattr(value, which) if which else None


@pytest.fixture
def receiver() -> Receiver:
    return Receiver()


@pytest.fixture
def totem(receiver: Receiver):
    """An initialized SDK whose exports land in ``receiver``."""
    recorder = tm.init(
        endpoint="http://totem.test",
        api_key="test-key",
        agent_id="test-agent",
        salt=SALT,
        side_effect_tools={"send_email"},
        external_tools={"web_fetch"},
        trusted_tools={"lookup_contact"},
        http_client=httpx.Client(transport=receiver.transport()),
    )
    yield recorder
    tm.shutdown(timeout=5.0)


def flush(timeout: float = 5.0) -> None:
    """Waits for the exporter to drain."""
    recorder = tm.recorder()
    assert recorder is not None
    assert recorder.exporter.flush(timeout), "exporter did not drain"


def parse(span: dict[str, Any], key: str) -> Any:
    """Decodes a JSON-valued attribute."""
    return json.loads(span[key])
