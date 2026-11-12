"""The store wrapper records what the agent remembers and recalls."""

from __future__ import annotations

from langgraph.store.memory import InMemoryStore

import totem_ai as tm
from totem_ai._store import value_text

from .conftest import Receiver, flush, parse

MEMORY = "Invoices for Acme go to billing@acme-payments.example"


def test_value_text_unwraps_the_common_shapes() -> None:
    assert value_text("plain") == "plain"
    assert value_text({"value": MEMORY}) == MEMORY
    assert value_text({"a": "one", "b": "two"}) == "one\ntwo"
    assert value_text({"count": 3}) == '{"count":3}'


def test_put_records_a_memory_write(totem, receiver: Receiver) -> None:
    store = tm.wrap_store(InMemoryStore())
    with tm.run():
        store.put(("vendor", "acme"), "invoice_contact", {"value": MEMORY})
    flush()

    write = receiver.one("memory.write")
    assert write["totem.memory.id"] == "vendor/acme/invoice_contact"
    assert "[EMAIL:" in write["totem.content"]
    assert write["totem.memory.deleted"] is False


def test_delete_records_a_deletion(totem, receiver: Receiver) -> None:
    store = tm.wrap_store(InMemoryStore())
    with tm.run():
        store.put(("vendor", "acme"), "invoice_contact", {"value": MEMORY})
        store.delete(("vendor", "acme"), "invoice_contact")
    flush()

    writes = receiver.ops("memory.write")
    assert len(writes) == 2
    assert writes[1]["totem.memory.deleted"] is True


def test_get_records_a_read(totem, receiver: Receiver) -> None:
    store = tm.wrap_store(InMemoryStore())
    with tm.run():
        store.put(("vendor", "acme"), "invoice_contact", {"value": MEMORY})
        assert store.get(("vendor", "acme"), "invoice_contact") is not None
    flush()

    results = parse(receiver.one("memory.read"), "totem.read.results")
    assert len(results) == 1
    assert results[0]["memory_id"] == "vendor/acme/invoice_contact"
    assert results[0]["hmac"] == receiver.ops("memory.write")[0]["totem.hmac"]


def test_search_records_every_hit(totem, receiver: Receiver) -> None:
    store = tm.wrap_store(InMemoryStore())
    with tm.run():
        store.put(("vendor", "acme"), "a", {"value": MEMORY})
        store.put(("vendor", "acme"), "b", {"value": "Acme pays on net 30."})
        found = store.search(("vendor", "acme"))
    assert len(found) == 2
    flush()

    results = parse(receiver.ops("memory.read")[-1], "totem.read.results")
    assert {r["memory_id"] for r in results} == {"vendor/acme/a", "vendor/acme/b"}


def test_a_miss_records_nothing(totem, receiver: Receiver) -> None:
    store = tm.wrap_store(InMemoryStore())
    with tm.run():
        assert store.get(("vendor", "acme"), "missing") is None
    flush()
    assert receiver.ops("memory.read") == []


async def test_async_batch_is_recorded_too(totem, receiver: Receiver) -> None:
    store = tm.wrap_store(InMemoryStore())
    with tm.run():
        await store.aput(("vendor", "acme"), "invoice_contact", {"value": MEMORY})
        assert await store.aget(("vendor", "acme"), "invoice_contact") is not None
    flush()
    assert receiver.ops("memory.write")
    assert receiver.ops("memory.read")


def test_the_store_still_works_without_totem(receiver: Receiver) -> None:
    tm.shutdown()
    store = tm.wrap_store(InMemoryStore())
    store.put(("a",), "b", {"value": "c"})
    item = store.get(("a",), "b")
    assert item is not None and item.value == {"value": "c"}
