//! Property tests for the invariants the rest of Totem relies on.

use proptest::prelude::*;
use totem_core::{hmac, match_in_context, normalize, redact};

proptest! {
    /// Normalizing twice changes nothing, so a fingerprint is stable however
    /// often text passes through the pipeline.
    #[test]
    fn normalize_is_idempotent(s in ".{0,400}") {
        let once = normalize(&s);
        prop_assert_eq!(normalize(&once), once);
    }

    /// Whitespace never changes a fingerprint.
    #[test]
    fn hmac_ignores_whitespace_runs(a in "[a-zA-Z0-9]{1,40}", b in "[a-zA-Z0-9]{1,40}") {
        prop_assert_eq!(hmac("salt", &format!("{a} {b}")), hmac("salt", &format!("{a}   \n\t {b}")));
    }

    /// Redacting already-redacted text is a no-op. The server depends on this:
    /// it re-runs redaction on content the SDK already redacted and must get
    /// the same fingerprint back.
    #[test]
    fn redaction_is_idempotent(s in ".{0,400}") {
        let (once, _) = redact("salt", &s);
        let (twice, findings) = redact("salt", &once);
        prop_assert_eq!(&twice, &once);
        prop_assert!(findings.is_empty(), "second pass found {:?} in {:?}", findings, once);
    }

    /// Text that is dropped verbatim into a message is always found, and the
    /// reported span points at it.
    #[test]
    fn verbatim_text_is_always_found(
        memory in "[a-zA-Z][a-zA-Z0-9 ]{10,80}",
        before in "[a-zA-Z0-9 ]{0,40}",
        after in "[a-zA-Z0-9 ]{0,40}",
    ) {
        let message = format!("{before}|{memory}|{after}");
        let matches = match_in_context(&[("m".to_string(), memory.clone())], std::slice::from_ref(&message));
        prop_assert_eq!(matches.len(), 1, "no match for {:?} in {:?}", memory, message);
        prop_assert_eq!(format!("{:?}", matches[0].match_kind), "Exact");
        let (index, start, end) = matches[0].spans[0];
        prop_assert_eq!(index, 0);
        let slice: String = message.chars().skip(start).take(end - start).collect();
        prop_assert_eq!(normalize(&slice).to_lowercase(), normalize(&memory).to_lowercase());
    }

    /// A candidate that shares no words with the prompt is never reported.
    #[test]
    fn disjoint_text_never_matches(n in 1usize..40) {
        let memory = "zzqq ".repeat(n);
        let message = "alpha beta gamma delta epsilon zeta eta theta iota kappa".to_string();
        prop_assert!(match_in_context(&[("m".to_string(), memory)], &[message]).is_empty());
    }
}
