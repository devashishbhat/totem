//! Influence matching: did a candidate text reach the prompt, and did it
//! supply a value to a tool argument?
//!
//! Both answers are evidence, not proof, which is why a match is labelled
//! `exact` or `partial` and why replay exists.

use std::cell::RefCell;
use std::collections::{HashMap, HashSet};
use std::rc::Rc;

use once_cell::sync::Lazy;
use regex::Regex;
use serde::Serialize;
use serde_json::Value;

use crate::text::{
    fold, is_word_char, normalize, shingle_size, shingles, word_hash, words, Folded, Word,
    PARTIAL_THRESHOLD,
};

/// The partial-match threshold, re-exported so the Python module can show it.
pub const PARTIAL_THRESHOLD_EXPORT: f64 = PARTIAL_THRESHOLD;

/// How strongly a candidate matched.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Match {
    /// The candidate's text appears verbatim.
    Exact,
    /// Most of the candidate's eight-word shingles appear.
    Partial,
}

impl Match {
    fn rank(self) -> u8 {
        match self {
            Match::Exact => 2,
            Match::Partial => 1,
        }
    }
}

/// `(message index, start code point, end code point)` into a message.
pub type Span = (usize, usize, usize);

/// The running best match for one candidate: level, coverage and spans.
type Best = Option<(Match, f64, Vec<Span>)>;

/// A candidate match inside the prompt.
#[derive(Debug, Clone, Serialize)]
pub struct ContextMatch {
    /// The candidate's id, as the caller supplied it.
    pub id: String,
    #[serde(rename = "match")]
    pub match_kind: Match,
    /// The share of the candidate's shingles that were found; 1.0 for exact.
    pub coverage: f64,
    /// `(message index, start code point, end code point)` ranges into the
    /// original message text. Replay cuts exactly these out.
    pub spans: Vec<Span>,
}

/// Finds which candidates reached the prompt.
///
/// `candidates` is a list of `(id, text)`; `messages` is the message contents
/// of the request, in the order they appear in it, so the message index in a
/// span addresses the recorded payload directly.
pub fn match_in_context(candidates: &[(String, String)], messages: &[String]) -> Vec<ContextMatch> {
    // Everything that depends only on the prompt is computed once, because a
    // run can carry dozens of candidate memories against a prompt of a
    // hundred thousand characters.
    let folded_candidates: Vec<(&String, Folded)> = candidates
        .iter()
        .map(|(id, text)| (id, fold(text)))
        .filter(|(_, f)| !f.chars.is_empty())
        .collect();
    let candidate_parts: Vec<(&String, &Folded, Vec<Word>)> = folded_candidates
        .iter()
        .map(|(id, f)| (*id, f, words(&f.chars)))
        .collect();
    let prepared: Vec<PreparedMessage> = messages.iter().map(|m| PreparedMessage::new(m)).collect();

    let mut out = Vec::new();
    for (id, folded, candidate_words) in candidate_parts {
        let size = shingle_size(candidate_words.len());
        let mut distinct: Vec<u64> = shingles(&folded.chars, &candidate_words, size)
            .into_iter()
            .map(|(k, _)| k)
            .collect();
        distinct.sort_unstable();
        distinct.dedup();
        let mut candidate_word_hashes: Vec<u64> = candidate_words
            .iter()
            .map(|w| word_hash(&folded.chars, *w))
            .collect();
        candidate_word_hashes.sort_unstable();
        candidate_word_hashes.dedup();
        let mut best: Best = None;

        for (index, message) in prepared.iter().enumerate() {
            // Verbatim inclusion implies every word of the candidate appears
            // in the prompt, so the word index rules most candidates out
            // before any scanning happens.
            let shared = candidate_word_hashes
                .iter()
                .filter(|w| message.word_hashes.contains(*w))
                .count();
            let worth_scanning = shared == candidate_word_hashes.len();
            if worth_scanning {
                let exact_spans: Vec<Span> = find_all(&message.folded.chars, &folded.chars)
                    .into_iter()
                    .filter_map(|(s, e)| {
                        message
                            .folded
                            .original_range(s, e)
                            .map(|(a, b)| (index, a, b))
                    })
                    .collect();
                if !exact_spans.is_empty() {
                    accumulate(&mut best, Match::Exact, 1.0, exact_spans);
                    continue;
                }
            }

            if distinct.is_empty() {
                continue;
            }
            // Every shingle that matches needs all of its words present, so a
            // candidate that shares too few words cannot reach the threshold
            // and the prompt's shingle index is never built for it.
            if (shared as f64) < PARTIAL_THRESHOLD * candidate_word_hashes.len() as f64 {
                continue;
            }
            let message_shingles = message.shingle_index(size);
            let mut found: Vec<Word> = Vec::new();
            for key in &distinct {
                if let Some(spans) = message_shingles.get(key) {
                    found.push(spans[0]);
                }
            }
            let coverage = found.len() as f64 / distinct.len() as f64;
            if coverage >= PARTIAL_THRESHOLD {
                let spans = merge_spans(&message.folded, index, &mut found);
                accumulate(&mut best, Match::Partial, coverage, spans);
            }
        }

        if let Some((kind, coverage, spans)) = best {
            out.push(ContextMatch {
                id: id.clone(),
                match_kind: kind,
                coverage,
                spans,
            });
        }
    }
    out
}

/// A shingle index: hash of an n-word window to the folded ranges it covers.
type ShingleIndex = Rc<HashMap<u64, Vec<Word>>>;

/// One prompt message, folded once with its words indexed.
///
/// The shingle index is built on first use, per shingle size, because most
/// candidates are ruled out by the word index and never need it.
struct PreparedMessage {
    folded: Folded,
    words: Vec<Word>,
    word_hashes: HashSet<u64>,
    shingles: RefCell<HashMap<usize, ShingleIndex>>,
}

impl PreparedMessage {
    fn new(text: &str) -> Self {
        let folded = fold(text);
        let message_words = words(&folded.chars);
        let word_hashes = message_words
            .iter()
            .map(|w| word_hash(&folded.chars, *w))
            .collect();
        PreparedMessage {
            folded,
            words: message_words,
            word_hashes,
            shingles: RefCell::new(HashMap::new()),
        }
    }

    fn shingle_index(&self, size: usize) -> ShingleIndex {
        if let Some(existing) = self.shingles.borrow().get(&size) {
            return Rc::clone(existing);
        }
        let mut index: HashMap<u64, Vec<Word>> = HashMap::new();
        for (key, span) in shingles(&self.folded.chars, &self.words, size) {
            index.entry(key).or_default().push(span);
        }
        let shared = Rc::new(index);
        self.shingles.borrow_mut().insert(size, Rc::clone(&shared));
        shared
    }
}

/// A value that flowed from a candidate into a tool argument.
#[derive(Debug, Clone, Serialize)]
pub struct ValueFlow {
    pub id: String,
    /// Dotted path of the argument, for example `to` or `recipients[0]`.
    pub arg_path: String,
    #[serde(rename = "match")]
    pub match_kind: Match,
}

/// Minimum length of a single-word argument value worth attributing. One
/// common word ("invoice", "October") appears in half the prompt and says
/// nothing about where the value came from; a long token is distinctive.
const MIN_SINGLE_WORD_LEN: usize = 8;

static CONTACT: Lazy<Regex> = Lazy::new(|| {
    Regex::new(
        r"(?i)(?:[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}|https?://[^\s<>\x22']+|\[(?:EMAIL|PHONE):[0-9a-f]{6}\])",
    )
    .unwrap()
});

/// Finds which candidates supplied the values in a tool call's arguments.
///
/// `args_json` is the arguments object. Contact-shaped values (emails, URLs
/// and their placeholders) must match exactly, because a near-miss address is
/// a different address; other text may match partially.
pub fn match_value_flow(args_json: &str, candidates: &[(String, String)]) -> Vec<ValueFlow> {
    let parsed: Value = match serde_json::from_str(args_json) {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let mut leaves = Vec::new();
    collect_leaves("", &parsed, &mut leaves);

    let folded_candidates: Vec<(String, Folded)> = candidates
        .iter()
        .map(|(id, text)| (id.clone(), fold(text)))
        .collect();

    let mut out = Vec::new();
    for (path, value) in leaves {
        let contacts: Vec<String> = CONTACT
            .find_iter(&value)
            .map(|m| normalize(m.as_str()).to_lowercase())
            .collect();
        let folded_value = fold(&value);
        let value_words = words(&folded_value.chars);
        if !is_attributable(&value, value_words.len(), !contacts.is_empty()) {
            continue;
        }
        let value_size = shingle_size(value_words.len());
        let value_shingles = shingles(&folded_value.chars, &value_words, value_size);

        for (id, candidate) in &folded_candidates {
            let haystack = candidate.text();
            if !contacts.is_empty() {
                // A contact value counts only when the same address appears in
                // the candidate.
                if contacts.iter().any(|c| haystack.contains(c)) {
                    out.push(ValueFlow {
                        id: id.clone(),
                        arg_path: path.clone(),
                        match_kind: Match::Exact,
                    });
                }
                continue;
            }
            if !folded_value.chars.is_empty()
                && contains_whole_words(&candidate.chars, &folded_value.chars)
            {
                out.push(ValueFlow {
                    id: id.clone(),
                    arg_path: path.clone(),
                    match_kind: Match::Exact,
                });
                continue;
            }
            if value_shingles.is_empty() {
                continue;
            }
            let candidate_words = words(&candidate.chars);
            let candidate_keys: HashSet<u64> =
                shingles(&candidate.chars, &candidate_words, value_size)
                    .into_iter()
                    .map(|(k, _)| k)
                    .collect();
            let distinct: HashSet<u64> = value_shingles.iter().map(|(k, _)| *k).collect();
            let hits = distinct
                .iter()
                .filter(|k| candidate_keys.contains(*k))
                .count();
            if hits as f64 / distinct.len() as f64 >= PARTIAL_THRESHOLD {
                out.push(ValueFlow {
                    id: id.clone(),
                    arg_path: path.clone(),
                    match_kind: Match::Partial,
                });
            }
        }
    }
    out
}

/// Whether an argument value is distinctive enough to attribute at all.
///
/// A contact detail always is: a redirected address is the whole point. So is
/// a phrase, or a single long token. A single short word is not.
fn is_attributable(value: &str, word_count: usize, has_contact: bool) -> bool {
    has_contact || word_count >= 2 || value.chars().count() >= MIN_SINGLE_WORD_LEN
}

fn collect_leaves(prefix: &str, value: &Value, out: &mut Vec<(String, String)>) {
    match value {
        Value::Object(map) => {
            for (key, child) in map {
                let path = if prefix.is_empty() {
                    key.clone()
                } else {
                    format!("{prefix}.{key}")
                };
                collect_leaves(&path, child, out);
            }
        }
        Value::Array(items) => {
            for (i, child) in items.iter().enumerate() {
                collect_leaves(&format!("{prefix}[{i}]"), child, out);
            }
        }
        Value::String(s) => out.push((prefix.to_string(), s.clone())),
        _ => {}
    }
}

/// Every occurrence of `needle` in `haystack`, as half-open code-point
/// ranges. Both sides are already folded, so this is a plain scan with a
/// first-character skip.
fn find_all(haystack: &[char], needle: &[char]) -> Vec<(usize, usize)> {
    if needle.is_empty() || needle.len() > haystack.len() {
        return Vec::new();
    }
    let first = needle[0];
    let mut out = Vec::new();
    let mut i = 0;
    while i + needle.len() <= haystack.len() {
        if haystack[i] != first {
            i += 1;
            continue;
        }
        if haystack[i..i + needle.len()] == needle[..] {
            out.push((i, i + needle.len()));
            i += needle.len();
            continue;
        }
        i += 1;
    }
    out
}

/// Whether `needle` appears in `haystack` on word boundaries.
///
/// Plain substring matching would attribute an argument of "invoice" to a
/// memory that merely says "invoices", which is a value the memory never
/// supplied.
fn contains_whole_words(haystack: &[char], needle: &[char]) -> bool {
    find_all(haystack, needle).into_iter().any(|(start, end)| {
        let before_ok = start == 0 || !is_word_char(haystack[start - 1]);
        let after_ok = end >= haystack.len() || !is_word_char(haystack[end]);
        before_ok && after_ok
    })
}

fn accumulate(best: &mut Best, kind: Match, coverage: f64, mut spans: Vec<Span>) {
    match best {
        Some((existing, existing_coverage, existing_spans)) if existing.rank() == kind.rank() => {
            existing_spans.append(&mut spans);
            if coverage > *existing_coverage {
                *existing_coverage = coverage;
            }
        }
        Some((existing, _, _)) if existing.rank() > kind.rank() => {}
        _ => *best = Some((kind, coverage, spans)),
    }
}

/// Merges the folded word ranges of matched shingles into as few original
/// ranges as possible, so replay removes whole phrases rather than fragments.
fn merge_spans(
    folded: &Folded,
    message_index: usize,
    found: &mut [crate::text::Word],
) -> Vec<Span> {
    found.sort_by_key(|w| w.start);
    let mut merged: Vec<(usize, usize)> = Vec::new();
    for w in found.iter() {
        match merged.last_mut() {
            Some(last) if w.start <= last.1 => {
                if w.end > last.1 {
                    last.1 = w.end;
                }
            }
            _ => merged.push((w.start, w.end)),
        }
    }
    merged
        .into_iter()
        .filter_map(|(s, e)| {
            folded
                .original_range(s, e)
                .map(|(os, oe)| (message_index, os, oe))
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    const MEMORY: &str = "Invoices for Acme go to billing@acme-payments.example";

    #[test]
    fn exact_match_reports_the_original_span() {
        let messages = vec![
            "You are a CRM assistant.".to_string(),
            format!("Remembered: {MEMORY}\nSend the invoice."),
        ];
        let candidates = vec![("mem1".to_string(), MEMORY.to_string())];
        let matches = match_in_context(&candidates, &messages);
        assert_eq!(matches.len(), 1);
        assert_eq!(matches[0].match_kind, Match::Exact);
        let (index, start, end) = matches[0].spans[0];
        assert_eq!(index, 1);
        let slice: String = messages[1].chars().skip(start).take(end - start).collect();
        assert_eq!(slice, MEMORY);
    }

    #[test]
    fn a_reworded_memory_matches_partially() {
        let long = "the finance team approves every Acme invoice on the first Friday of the month";
        let messages = vec![
            "Note: the finance team approves every Acme invoice on the first Friday of each month."
                .to_string(),
        ];
        let matches = match_in_context(&[("mem1".to_string(), long.to_string())], &messages);
        assert_eq!(matches.len(), 1, "{matches:?}");
        assert_eq!(matches[0].match_kind, Match::Partial);
        assert!(matches[0].coverage >= PARTIAL_THRESHOLD);
    }

    #[test]
    fn unrelated_text_does_not_match() {
        let messages = vec!["The weather in Oslo is cold today.".to_string()];
        assert!(
            match_in_context(&[("mem1".to_string(), MEMORY.to_string())], &messages).is_empty()
        );
    }

    #[test]
    fn value_flow_finds_the_address_argument() {
        let args =
            r#"{"to": "billing@acme-payments.example", "body": "October invoice attached."}"#;
        let flows = match_value_flow(args, &[("mem1".to_string(), MEMORY.to_string())]);
        assert_eq!(flows.len(), 1, "{flows:?}");
        assert_eq!(flows[0].arg_path, "to");
        assert_eq!(flows[0].match_kind, Match::Exact);
    }

    #[test]
    fn value_flow_ignores_a_different_address() {
        let args = r#"{"to": "ap@acme.example"}"#;
        assert!(match_value_flow(args, &[("mem1".to_string(), MEMORY.to_string())]).is_empty());
    }

    #[test]
    fn value_flow_requires_whole_words() {
        // "invoice" occurs inside "Invoices", but the memory never supplied
        // that value.
        let args = r#"{"body": "invoice"}"#;
        assert!(match_value_flow(args, &[("mem1".to_string(), MEMORY.to_string())]).is_empty());
    }

    #[test]
    fn value_flow_skips_a_single_short_word() {
        // "ap" and "invoice" are not evidence of anything.
        for args in [r#"{"to": "ap"}"#, r#"{"body": "invoice"}"#] {
            assert!(
                match_value_flow(args, &[("mem1".to_string(), MEMORY.to_string())]).is_empty(),
                "{args} should not attribute"
            );
        }
    }
}
