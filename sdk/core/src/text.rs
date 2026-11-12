//! Normalization and fingerprinting.
//!
//! These two functions decide when two pieces of text are "the same" for
//! Totem, so the Go server implements them identically; the rules are written
//! down once in `docs/text-rules.md`.

use std::hash::{DefaultHasher, Hash, Hasher};

use hmac::{Hmac, Mac};
use sha2::Sha256;
use unicode_normalization::UnicodeNormalization;

/// Applies NFKC, trims, and collapses every run of whitespace to one space.
pub fn normalize(text: &str) -> String {
    let composed: String = text.nfkc().collect();
    let mut out = String::with_capacity(composed.len());
    let mut pending_space = false;
    for ch in composed.chars() {
        if ch.is_whitespace() {
            pending_space = !out.is_empty();
            continue;
        }
        if pending_space {
            out.push(' ');
            pending_space = false;
        }
        out.push(ch);
    }
    out
}

/// Hex HMAC-SHA256 of the normalized text under the project salt.
pub fn hmac(salt: &str, text: &str) -> String {
    let mut mac =
        Hmac::<Sha256>::new_from_slice(salt.as_bytes()).expect("HMAC accepts a key of any length");
    mac.update(normalize(text).as_bytes());
    hex::encode(mac.finalize().into_bytes())
}

/// The identity of a piece of observed text: the HMAC of its redacted form.
/// The server can recompute it from the redacted content it stores, because
/// redaction is idempotent.
pub fn fingerprint(salt: &str, text: &str) -> String {
    let (redacted, _) = crate::redact::redact(salt, text);
    hmac(salt, &redacted)
}

/// A string folded for matching, with a map back to the original text.
///
/// Folding lowercases, applies NFKC per character and collapses whitespace.
/// Doing it per character keeps `map` exact, so a match found in the folded
/// text can always be reported as code-point offsets into the original.
pub struct Folded {
    /// The folded characters.
    pub chars: Vec<char>,
    /// `map[i]` is the code-point index in the original text that folded
    /// character `i` came from.
    pub map: Vec<usize>,
}

impl Folded {
    /// The folded text as a string, for substring search.
    pub fn text(&self) -> String {
        self.chars.iter().collect()
    }

    /// Translates a half-open range of folded characters into a half-open
    /// range of original code points.
    pub fn original_range(&self, start: usize, end: usize) -> Option<(usize, usize)> {
        if end == 0 || start >= self.map.len() || end > self.map.len() {
            return None;
        }
        Some((self.map[start], self.map[end - 1] + 1))
    }
}

/// Folds text for matching. See [`Folded`].
pub fn fold(text: &str) -> Folded {
    let mut chars = Vec::new();
    let mut map = Vec::new();
    let mut pending_space = false;
    for (index, ch) in text.chars().enumerate() {
        if ch.is_whitespace() {
            pending_space = !chars.is_empty();
            continue;
        }
        if pending_space {
            chars.push(' ');
            map.push(index);
            pending_space = false;
        }
        // ASCII is by far the common case and NFKC leaves it alone, so it
        // skips the normalization machinery entirely. Folding one character at
        // a time is what keeps `map` exact.
        if ch.is_ascii() {
            chars.push(ch.to_ascii_lowercase());
            map.push(index);
            continue;
        }
        for folded in ch.nfkc().flat_map(|c| c.to_lowercase()) {
            chars.push(folded);
            map.push(index);
        }
    }
    Folded { chars, map }
}

/// One word of folded text, with the folded range it occupies.
#[derive(Debug, Clone, Copy)]
pub struct Word {
    /// Index of the first folded character.
    pub start: usize,
    /// Index one past the last folded character.
    pub end: usize,
}

/// Whether a character belongs to a word. An email address, a URL or a dotted
/// identifier holds together as one word, because each is a single value.
pub fn is_word_char(ch: char) -> bool {
    ch.is_alphanumeric() || matches!(ch, '@' | '.' | '-' | '_' | '+' | ':' | '/')
}

/// Splits folded characters into words on whitespace and punctuation.
pub fn words(chars: &[char]) -> Vec<Word> {
    let mut out = Vec::new();
    let mut start = None;
    for (i, ch) in chars.iter().enumerate() {
        let part_of_word = is_word_char(*ch);
        match (part_of_word, start) {
            (true, None) => start = Some(i),
            (false, Some(s)) => {
                out.push(Word { start: s, end: i });
                start = None;
            }
            _ => {}
        }
    }
    if let Some(s) = start {
        out.push(Word {
            start: s,
            end: chars.len(),
        });
    }
    out
}

/// The shingle size for partial matching: eight words, or the whole text when
/// it is shorter, so a short memory can still be matched.
pub const SHINGLE: usize = 8;

/// The fraction of a candidate's shingles that must appear for a partial match.
pub const PARTIAL_THRESHOLD: f64 = 0.6;

/// The shingle size to use for a text of `word_count` words: eight, or the
/// whole text when it is shorter. Both sides of a comparison must shingle at
/// the same size for their keys to be comparable.
pub fn shingle_size(word_count: usize) -> usize {
    SHINGLE.min(word_count).max(1)
}

/// The hash of one word, used to test cheaply whether a word appears at all.
pub fn word_hash(chars: &[char], word: Word) -> u64 {
    let mut hasher = DefaultHasher::new();
    for ch in &chars[word.start..word.end] {
        ch.hash(&mut hasher);
    }
    hasher.finish()
}

/// Builds the `n`-word shingles of folded text as hashes, each with the folded
/// range it covers.
///
/// Shingles are hashed rather than materialized as strings: a prompt of a
/// hundred thousand characters has tens of thousands of windows, and building
/// a string for each one dominated the matching cost. A hash collision can
/// only cause a partial match to be scanned and rejected, never a wrong exact
/// match, because exact matching compares the text itself.
pub fn shingles(chars: &[char], words: &[Word], n: usize) -> Vec<(u64, Word)> {
    if words.is_empty() || n == 0 || n > words.len() {
        return Vec::new();
    }
    let mut out = Vec::with_capacity(words.len() - n + 1);
    for start in 0..=words.len() - n {
        let span = Word {
            start: words[start].start,
            end: words[start + n - 1].end,
        };
        let mut hasher = DefaultHasher::new();
        for word in &words[start..start + n] {
            for ch in &chars[word.start..word.end] {
                ch.hash(&mut hasher);
            }
            // A separator keeps "ab cd" from hashing like "abc d".
            0u8.hash(&mut hasher);
        }
        out.push((hasher.finish(), span));
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_collapses_and_trims() {
        assert_eq!(normalize("  a \n\t b  "), "a b");
        assert_eq!(normalize(""), "");
        assert_eq!(normalize("\u{fb01}x"), "fix");
    }

    #[test]
    fn hmac_is_stable_and_salted() {
        assert_eq!(hmac("s", "a b"), hmac("s", " a   b "));
        assert_ne!(hmac("s", "a b"), hmac("t", "a b"));
        assert_eq!(hmac("s", "a").len(), 64);
    }

    #[test]
    fn fold_maps_back_to_the_original() {
        let f = fold("Hello   World");
        assert_eq!(f.text(), "hello world");
        let (start, end) = f.original_range(6, 11).unwrap();
        assert_eq!(
            "Hello   World"
                .chars()
                .skip(start)
                .take(end - start)
                .collect::<String>(),
            "World"
        );
    }

    #[test]
    fn words_keep_email_shapes_together() {
        let f = fold("mail to billing@acme-payments.example now");
        let w = words(&f.chars);
        let texts: Vec<String> = w
            .iter()
            .map(|x| f.chars[x.start..x.end].iter().collect())
            .collect();
        assert!(
            texts.contains(&"billing@acme-payments.example".to_string()),
            "{texts:?}"
        );
    }
}
