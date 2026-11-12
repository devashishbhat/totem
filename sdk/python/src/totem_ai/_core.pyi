"""Type stubs for the Rust core (``sdk/core``)."""

from typing import TypedDict

PARTIAL_THRESHOLD: float

class Finding(TypedDict):
    kind: str
    placeholder: str
    start: int
    end: int

class ContextMatch(TypedDict):
    id: str
    match: str
    coverage: float
    spans: list[tuple[int, int, int]]

class ValueFlow(TypedDict):
    id: str
    arg_path: str
    match: str

def normalize(text: str) -> str: ...
def hmac(salt: str, text: str) -> str: ...
def fingerprint(salt: str, text: str) -> str: ...
def redact(salt: str, text: str) -> tuple[str, list[Finding]]: ...
def has_contact_placeholder(text: str) -> bool: ...
def canonical_json(value: str) -> str: ...
def match_in_context(
    candidates: list[tuple[str, str]], messages: list[str]
) -> list[ContextMatch]: ...
def match_value_flow(args_json: str, candidates: list[tuple[str, str]]) -> list[ValueFlow]: ...
