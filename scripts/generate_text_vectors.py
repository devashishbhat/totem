"""Regenerates the cross-language text vectors from the Rust core.

The Go server must agree with the Rust SDK on normalization, fingerprinting,
redaction and canonical JSON. These vectors are what pins them together; see
server/internal/totemtext/testdata/README.md.

Run it through the SDK's virtualenv, which has the compiled core:

    make text-vectors
"""

import json
import pathlib

from totem_ai import _core

SALT = "cross-language-salt"

INPUTS = [
    "",
    "   ",
    "a b",
    "  a \n\t b  ",
    "Acme Supplies",
    "ﬁx",
    "a b",
    "Invoices for Acme go to billing@acme-payments.example",
    "Please remember that invoices for Acme go to billing@acme-payments.example from now on.",
    "ap@acme.example and billing@acme-payments.example",
    "card 4111 1111 1111 1111",
    "order 1234567890123456789",
    "key sk-abcdefghijklmnopqrstuvwxyz0123",
    "call 415-555-0198 today",
    "token eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0."
    "dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
    "Invoices go to [EMAIL:3f9a1c]",
    "Acme pays on net 30.",
]

JSON_INPUTS = [
    '{ "b": 1, "a": [2, 3] }',
    "[1.0, 2.5]",
    '{"b":null,"a":true}',
    '{"\\u00e9":1,"a":2}',
    '{"to":"billing@acme-payments.example","body":"hi"}',
]

vectors = {
    "salt": SALT,
    "text": [
        {
            "input": text,
            "normalized": _core.normalize(text),
            "hmac": _core.hmac(SALT, text),
            "fingerprint": _core.fingerprint(SALT, text),
            "redacted": _core.redact(SALT, text)[0],
            "findings": [f["kind"] for f in _core.redact(SALT, text)[1]],
            "has_contact_placeholder": _core.has_contact_placeholder(
                _core.redact(SALT, text)[0]
            ),
        }
        for text in INPUTS
    ],
    "json": [
        {"input": value, "canonical": _core.canonical_json(value)} for value in JSON_INPUTS
    ],
}

path = pathlib.Path(__file__).resolve().parent.parent / "server/internal/totemtext/testdata/vectors.json"
path.write_text(json.dumps(vectors, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
print(f"wrote {path} with {len(INPUTS)} text and {len(JSON_INPUTS)} JSON vectors")
