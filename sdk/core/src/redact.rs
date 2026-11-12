//! Redaction: replaces secret-looking values with typed, stable placeholders
//! before anything leaves the process.
//!
//! The placeholder is `[EMAIL:3f9a1c]`, where the suffix is the first six hex
//! characters of the value's HMAC. The same value always produces the same
//! placeholder, so Totem can still show that a value flowed from a memory into
//! a tool argument without ever storing the value.

use once_cell::sync::Lazy;
use regex::Regex;
use serde::Serialize;

use crate::text::hmac;

/// The class of a redacted value.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub enum Kind {
    Email,
    Phone,
    Card,
    Key,
    Jwt,
    Token,
}

impl Kind {
    /// The name that appears in the placeholder.
    pub fn tag(self) -> &'static str {
        match self {
            Kind::Email => "EMAIL",
            Kind::Phone => "PHONE",
            Kind::Card => "CARD",
            Kind::Key => "KEY",
            Kind::Jwt => "JWT",
            Kind::Token => "TOKEN",
        }
    }
}

/// One redacted value.
#[derive(Debug, Clone, Serialize)]
pub struct Finding {
    pub kind: Kind,
    pub placeholder: String,
    /// Byte offset of the value in the input.
    pub start: usize,
    pub end: usize,
}

/// Minimum Shannon entropy, in bits per character, for a long opaque string to
/// count as a secret.
const ENTROPY_FLOOR: f64 = 3.5;

static PLACEHOLDER: Lazy<Regex> =
    Lazy::new(|| Regex::new(r"\[(?:EMAIL|PHONE|CARD|KEY|JWT|TOKEN):[0-9a-f]{6}\]").unwrap());
static JWT: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}").unwrap()
});
static KEY: Lazy<Regex> = Lazy::new(|| {
    Regex::new(concat!(
        r"(?:sk-(?:proj-)?[A-Za-z0-9_-]{16,}",
        r"|AKIA[0-9A-Z]{16}",
        r"|gh[pousr]_[A-Za-z0-9]{20,}",
        r"|xox[baprs]-[A-Za-z0-9-]{10,}",
        r"|AIza[A-Za-z0-9_-]{30,})"
    ))
    .unwrap()
});
static EMAIL: Lazy<Regex> = Lazy::new(|| {
    Regex::new(
        r"[A-Za-z0-9._%+-]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*\.[A-Za-z]{2,}",
    )
    .unwrap()
});
static CARD: Lazy<Regex> = Lazy::new(|| Regex::new(r"\b(?:\d[ -]?){12,18}\d\b").unwrap());
static PHONE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r"(?:\+\d{1,3}[ .-]?)?(?:\(\d{3}\)|\d{3})[ .-]\d{3}[ .-]\d{4}\b").unwrap()
});
static TOKEN: Lazy<Regex> = Lazy::new(|| Regex::new(r"[A-Za-z0-9+/=_-]{32,}").unwrap());

/// A detector hit, before overlaps are resolved.
struct Hit {
    kind: Option<Kind>,
    order: u8,
    start: usize,
    end: usize,
}

/// Replaces every secret-looking value with its placeholder.
///
/// Redaction is idempotent: text that is already redacted comes back
/// unchanged, which is what lets the SDK and the server agree on a
/// fingerprint even though only the SDK sees the original.
pub fn redact(salt: &str, text: &str) -> (String, Vec<Finding>) {
    let mut hits: Vec<Hit> = Vec::new();
    collect(&PLACEHOLDER, None, 0, text, &mut hits, |_| true);
    collect(&JWT, Some(Kind::Jwt), 1, text, &mut hits, |_| true);
    collect(&KEY, Some(Kind::Key), 2, text, &mut hits, |_| true);
    collect(&EMAIL, Some(Kind::Email), 3, text, &mut hits, |_| true);
    collect(&CARD, Some(Kind::Card), 4, text, &mut hits, is_luhn);
    collect(&PHONE, Some(Kind::Phone), 5, text, &mut hits, |_| true);
    collect(
        &TOKEN,
        Some(Kind::Token),
        6,
        text,
        &mut hits,
        has_secret_entropy,
    );

    // Highest priority first, then leftmost, then longest, so the result never
    // depends on the order the detectors ran in.
    hits.sort_by(|a, b| {
        a.order
            .cmp(&b.order)
            .then(a.start.cmp(&b.start))
            .then(b.end.cmp(&a.end))
    });
    let mut kept: Vec<Hit> = Vec::new();
    for hit in hits {
        if kept.iter().any(|k| hit.start < k.end && k.start < hit.end) {
            continue;
        }
        kept.push(hit);
    }
    kept.sort_by_key(|h| h.start);

    let mut out = String::with_capacity(text.len());
    let mut findings = Vec::new();
    let mut last = 0;
    for hit in &kept {
        out.push_str(&text[last..hit.start]);
        match hit.kind {
            None => out.push_str(&text[hit.start..hit.end]),
            Some(kind) => {
                let p = placeholder(salt, kind, &text[hit.start..hit.end]);
                out.push_str(&p);
                findings.push(Finding {
                    kind,
                    placeholder: p,
                    start: hit.start,
                    end: hit.end,
                });
            }
        }
        last = hit.end;
    }
    out.push_str(&text[last..]);
    (out, findings)
}

/// The stable placeholder for one value.
pub fn placeholder(salt: &str, kind: Kind, value: &str) -> String {
    format!("[{}:{}]", kind.tag(), &hmac(salt, value)[..6])
}

fn collect(
    re: &Regex,
    kind: Option<Kind>,
    order: u8,
    text: &str,
    hits: &mut Vec<Hit>,
    accept: impl Fn(&str) -> bool,
) {
    for m in re.find_iter(text) {
        if !accept(m.as_str()) {
            continue;
        }
        hits.push(Hit {
            kind,
            order,
            start: m.start(),
            end: m.end(),
        });
    }
}

fn is_luhn(s: &str) -> bool {
    let digits: Vec<u32> = s.chars().filter_map(|c| c.to_digit(10)).collect();
    if !(13..=19).contains(&digits.len()) {
        return false;
    }
    let mut sum = 0;
    for (i, d) in digits.iter().rev().enumerate() {
        let mut d = *d;
        if i % 2 == 1 {
            d *= 2;
            if d > 9 {
                d -= 9;
            }
        }
        sum += d;
    }
    sum % 10 == 0
}

fn has_secret_entropy(s: &str) -> bool {
    let mut counts = [0usize; 256];
    for b in s.bytes() {
        counts[b as usize] += 1;
    }
    let n = s.len() as f64;
    let entropy: f64 = counts
        .iter()
        .filter(|c| **c > 0)
        .map(|c| {
            let p = *c as f64 / n;
            -p * p.log2()
        })
        .sum();
    entropy >= ENTROPY_FLOOR
}

/// Whether text holds a redacted contact detail or a bare URL. The server
/// applies the same test when it decides whether an untrusted memory is worth
/// an alert.
pub fn has_contact_placeholder(text: &str) -> bool {
    text.contains("[EMAIL:")
        || text.contains("[PHONE:")
        || text.contains("http://")
        || text.contains("https://")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redacts_an_email_to_a_stable_placeholder() {
        let (out, findings) = redact("salt", "send it to billing@acme-payments.example now");
        assert_eq!(findings.len(), 1);
        assert_eq!(findings[0].kind, Kind::Email);
        assert!(out.starts_with("send it to [EMAIL:"), "{out}");
        let (again, _) = redact("salt", "mail billing@acme-payments.example");
        assert!(again.contains(&findings[0].placeholder));
    }

    #[test]
    fn different_values_get_different_placeholders() {
        let (a, _) = redact("salt", "ap@acme.example");
        let (b, _) = redact("salt", "billing@acme-payments.example");
        assert_ne!(a, b);
    }

    #[test]
    fn redaction_is_idempotent() {
        let (once, _) = redact(
            "salt",
            "card 4111 1111 1111 1111 and sk-abcdefghijklmnopqrstuvwx",
        );
        let (twice, findings) = redact("salt", &once);
        assert_eq!(once, twice);
        assert!(findings.is_empty(), "{findings:?}");
    }

    #[test]
    fn luhn_rejects_a_plain_number_run() {
        assert!(is_luhn("4111 1111 1111 1111"));
        assert!(!is_luhn("1234 5678 9012 3456"));
    }

    #[test]
    fn ordinary_prose_is_untouched() {
        let text = "Invoices for Acme are approved by the finance team every Friday.";
        let (out, findings) = redact("salt", text);
        assert_eq!(out, text);
        assert!(findings.is_empty());
    }
}
