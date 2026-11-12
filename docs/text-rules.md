# Text rules

These rules are implemented twice: in `sdk/core` (Rust, used by the SDK) and in
`server/internal/totemtext` (Go, used by the server). They have to agree
exactly, because a fingerprint computed in the SDK is how the server recognises
a memory, and a placeholder computed in the SDK is how the dashboard shows a
value travelling from a memory into a tool argument.

`server/internal/totemtext/testdata/vectors.json` is generated from the Rust
implementation and asserted against by both test suites. Regenerate it with
`make text-vectors` after changing anything here.

## Normalization

1. NFKC.
2. Collapse every run of whitespace to a single space.
3. Trim.

Normalization is idempotent.

## Fingerprint

`fingerprint(salt, text) = hmac_sha256_hex(salt, normalize(redact(salt, text)))`

The fingerprint is taken over the **redacted** text. The SDK sees the original
and the server only ever sees the redacted form, and because redaction is
idempotent both arrive at the same value.

## Redaction

Each detector finds candidate values. Overlaps are resolved by priority, then
leftmost, then longest, so the result never depends on the order the detectors
ran in.

| Priority | Kind | What it matches |
| --- | --- | --- |
| 0 | (existing placeholder) | `[EMAIL:3f9a1c]` and friends, left exactly as they are |
| 1 | `JWT` | three base64url segments beginning `eyJ` |
| 2 | `KEY` | `sk-…`, `AKIA…`, `ghp_…`, `xoxb-…`, `AIza…` |
| 3 | `EMAIL` | an address |
| 4 | `CARD` | 13 to 19 digits, with separators, that pass Luhn |
| 5 | `PHONE` | a grouped phone number |
| 6 | `TOKEN` | 32 or more base64url characters with Shannon entropy ≥ 3.5 bits per character |

Each match is replaced by `[KIND:xxxxxx]`, where `xxxxxx` is the first six hex
characters of `hmac_sha256_hex(salt, matched_value)`.

Redaction is idempotent: an existing placeholder is matched first and left
alone, and the six hex characters are too short to be caught by the token rule.

## Canonical JSON

RFC 8785: object keys sorted by UTF-16 code unit, no insignificant whitespace,
numbers in their shortest round-tripping form. Used to fingerprint JSON values
and to compare two sampled tool calls during replay.

## Matching (Rust only)

The matchers run inside the agent's process and never need to agree with the
server, so they fold text more aggressively than `normalize` does: per
character NFKC, lowercased, whitespace collapsed, with a map back to the
original code-point offsets. That map is what makes a reported span point at
the original text, which is what replay cuts out.

**In context.** A candidate matches `exact` when its folded text occurs
verbatim in a message. Otherwise it matches `partial` when at least 60% of its
eight-word shingles occur in one message. A candidate shorter than eight words
shingles at its own length, and both sides shingle at the same size.

**Value flow.** For each string leaf of the tool arguments:

- A contact-shaped value (an email, a URL, or one of their placeholders) must
  appear in the candidate exactly. A near-miss address is a different address.
- Any other value must be two or more words, or a single token of at least
  eight characters: one common word says nothing about where a value came from.
  A single-word value must match on word boundaries, so an argument of
  `invoice` is not attributed to a memory that says `invoices`.
