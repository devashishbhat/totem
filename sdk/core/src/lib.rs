//! `totem_core` holds the parts of the Totem SDK that must be fast and exactly
//! reproducible: text normalization, fingerprinting, redaction, canonical JSON
//! and influence matching.
//!
//! The Python package `totem_ai` imports it as `totem_ai._core`. The Go server
//! implements the same normalization, fingerprinting, redaction and canonical
//! JSON rules; they are written down in `docs/text-rules.md`.

pub mod canonical;
pub mod matching;
pub mod redact;
pub mod text;

pub use canonical::canonical_json;
pub use matching::{match_in_context, match_value_flow, ContextMatch, Match, ValueFlow};
pub use redact::{has_contact_placeholder, redact, Finding, Kind};
pub use text::{fingerprint, hmac, normalize};

#[cfg(feature = "python")]
mod python;
