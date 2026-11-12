"""The committed cross-language vectors must still match the Rust core.

The Go server's own test checks itself against the same file, so if either side
drifts one of the two suites fails rather than memories silently splitting in
two.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from totem_ai import _core

VECTORS = Path(__file__).resolve().parents[3] / "server/internal/totemtext/testdata/vectors.json"


def load() -> dict:
    if not VECTORS.exists():
        pytest.skip(f"{VECTORS} is missing")
    return json.loads(VECTORS.read_text(encoding="utf-8"))


def test_text_vectors_match() -> None:
    data = load()
    salt = data["salt"]
    assert data["text"], "no text vectors"
    for case in data["text"]:
        text = case["input"]
        assert _core.normalize(text) == case["normalized"], text
        assert _core.hmac(salt, text) == case["hmac"], text
        assert _core.fingerprint(salt, text) == case["fingerprint"], text
        redacted, findings = _core.redact(salt, text)
        assert redacted == case["redacted"], text
        assert [f["kind"] for f in findings] == case["findings"], text
        assert _core.has_contact_placeholder(redacted) == case["has_contact_placeholder"], text


def test_json_vectors_match() -> None:
    data = load()
    assert data["json"], "no JSON vectors"
    for case in data["json"]:
        assert _core.canonical_json(case["input"]) == case["canonical"], case["input"]
