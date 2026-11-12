# Architecture

Totem answers one question: **why did the agent do that?** It answers it by
recording what the agent read and what it then did, linking the two, and
proving the link by re-running the decision without the memory.

```text
 agent process                                  totem-server (one Go binary)
┌────────────────────────────┐                 ┌─────────────────────────────┐
│ LangGraph agent            │                 │ ingest  POST /v1/traces     │
│   store wrapped by Totem   │  OTLP/HTTP      │   validate, redact, append  │
│   callback handler         │ ──protobuf───▶  │                             │
│                            │                 │ lineage worker (goroutine)  │
│ totem_ai (Python)          │                 │   versions, edges, trust,   │
│   run context, exporter    │                 │   taint, attributions,      │
│ totem_core (Rust, PyO3)    │                 │   alerts                    │
│   normalize, hmac, redact, │                 │                             │
│   match in context,        │                 │ api     GET /api/v1/*       │
│   match value flow         │                 │ replay  goroutine per run   │
└────────────────────────────┘                 └──────────────┬──────────────┘
                                                              │
                                              ┌───────────────▼─────────────┐
                                              │ Postgres                    │
                                              │  events is also the queue   │
                                              └───────────────┬─────────────┘
                                                              │ JSON over HTTPS
                                              ┌───────────────▼─────────────┐
                                              │ dashboard (React SPA)       │
                                              └─────────────────────────────┘
```

There is no message bus. The `events` table is the queue: ingest inserts, and
the lineage worker claims rows with `FOR UPDATE SKIP LOCKED` in
`(occurred_at, seq)` order and marks them processed.

## The four claims, from weakest to strongest

Totem never says "this memory caused that decision" when it only knows
something weaker. Each level is recorded separately and shown separately.

| Level | What Totem saw | Where it comes from |
| --- | --- | --- |
| `retrieved` | The agent read this memory earlier in the run | the store wrapper |
| `in_context` | Its text was in the prompt of the call that made this decision | the Rust matcher, over the recorded messages |
| `value_flow` | Its text supplied the value of a specific argument | the Rust matcher, over the tool call's arguments |
| `counterfactual` | Removing it from the prompt changed what the agent decided | replay |

Only the last is evidence of cause, and only replay produces it.

## Trust and taint

Every piece of information that enters the agent is a **source** with a tier:

| Tier | What it is |
| --- | --- |
| T4 | the agent's own configuration |
| T3 | the user |
| T2 | a tool the operator listed as trusted |
| T1 | a document from outside, or any other tool |
| T0 | provenance unknown |

A memory version's `trust_min` is the minimum over its `DERIVED_FROM` parents,
and a version with no recorded parent is T0: nothing is known about where its
content came from. Its `taint_source_ids` are the T0 and T1 sources it descends
from.

A decision's `trust_min` is the minimum over what was **in its prompt** or
**supplied a value**. A memory that was only retrieved does not count against
it, because it never reached the model.

That gives the two alerts:

- `taint.side_effect_from_untrusted`: a world-changing tool call with
  `trust_min ≤ 1`. It is **critical** when untrusted text supplied an argument
  value, because then the attacker chose the value, not just the context.
- `memory.untrusted_contact_value`: an untrusted memory holding a contact
  detail, which is the shape a payment-redirect attack takes.

## Identity, and why nothing sensitive is stored

The SDK redacts before anything leaves the process. Each secret-looking value
becomes a typed, stable placeholder, `[EMAIL:3f9a1c]`, where the suffix is the
first six hex characters of the value's HMAC under the project salt. The same
value therefore shows the same placeholder everywhere, which is what lets the
dashboard show a value flowing from a memory into a tool argument without the
value ever being stored.

A piece of text is identified by its **fingerprint**: the HMAC of its *redacted*
normalized form. Redaction is idempotent, so the server can recompute the
fingerprint from the redacted content it holds and still agree with the SDK.
The rules are implemented twice, in Rust and in Go, and pinned together by
`server/internal/totemtext/testdata/vectors.json`.

## Replay

Replay takes the recorded Chat Completions request, cuts the target memory's
matched spans out of its messages (taking the whole line when a span covers
most of one), and sends both versions to the same model N times. Outputs are
compared by tool name plus canonical JSON arguments, against the modal baseline
output. The difference between the two arms' flip rates gets a 95% Newcombe
interval, and the verdict follows from the effect and that interval.

Replay never executes the agent's tools. It only asks the model.

Before grading anything, replay checks that the baseline arm reproduced the
decision the agent actually made. If it did not, the experiment cannot support
a claim about the memory and fails saying so.

That check has a sharp edge worth stating plainly: **redaction and replay pull
against each other.** The SDK redacts before export, so when the value that
drove a decision was itself a secret - an address, a key - the recorded prompt
holds a placeholder and the model can no longer act on it. The baseline then
does something else, and replay refuses to grade it. An operator who needs
replay on that class of decision sets `TM_REDACT_PAYLOADS=false` on the server
and `redact_payloads=False` in the SDK, which stores prompts as the agent sent
them. Everything else stays redacted either way, and sampled replay outputs are
redacted before they are stored, so turning it on widens what the prompt holds
and nothing else.

## What runs where

| Piece | Language | Why |
| --- | --- | --- |
| `sdk/core` | Rust | The matchers run on every model call inside the agent's own process: 100k characters against 50 candidate memories in under 2 ms. |
| `sdk/python` | Python | Where the agents are. |
| `server` | Go | One binary: ingest, worker, API and replay, with no runtime to install. |
| `dashboard` | React | A read-only view of one Postgres. |

## Things deliberately left out

- No message bus, no multi-service split, and no tenant columns. One project,
  one binary, one database.
- No embeddings and no vector search. Matching is lexical, which is why a match
  is labelled `exact` or `partial` and why replay exists.
- No blocking. Totem reports; it does not stand between the agent and its
  tools.

The rules the Rust and Go text primitives both implement are in
[text-rules.md](text-rules.md).
