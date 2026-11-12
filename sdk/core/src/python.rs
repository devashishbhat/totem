//! PyO3 bindings. The Python package imports this module as `totem_ai._core`.
//!
//! Results cross the boundary as plain Python dicts and lists, so the Python
//! side needs no type stubs beyond `_core.pyi` and nothing here holds the GIL
//! longer than the call itself.

use pyo3::prelude::*;
use pyo3::types::{PyDict, PyList};

use crate::matching::{Match, PARTIAL_THRESHOLD_EXPORT};
use crate::{canonical, matching, redact, text};

/// Applies NFKC, trims and collapses whitespace.
#[pyfunction]
#[pyo3(name = "normalize")]
fn py_normalize(text: &str) -> String {
    text::normalize(text)
}

/// Hex HMAC-SHA256 of the normalized text under the project salt.
#[pyfunction]
#[pyo3(name = "hmac")]
fn py_hmac(salt: &str, text: &str) -> String {
    text::hmac(salt, text)
}

/// The HMAC of the redacted text: the identity the server stores.
#[pyfunction]
#[pyo3(name = "fingerprint")]
fn py_fingerprint(salt: &str, text: &str) -> String {
    text::fingerprint(salt, text)
}

/// Replaces secrets with stable placeholders. Returns `(redacted, findings)`.
#[pyfunction]
#[pyo3(name = "redact")]
fn py_redact<'py>(
    py: Python<'py>,
    salt: &str,
    text: &str,
) -> PyResult<(String, Bound<'py, PyList>)> {
    let (out, findings) = redact::redact(salt, text);
    let list = PyList::empty(py);
    for f in findings {
        let d = PyDict::new(py);
        d.set_item("kind", f.kind.tag())?;
        d.set_item("placeholder", f.placeholder)?;
        d.set_item("start", f.start)?;
        d.set_item("end", f.end)?;
        list.append(d)?;
    }
    Ok((out, list))
}

/// Whether text holds a redacted contact detail or a bare URL.
#[pyfunction]
#[pyo3(name = "has_contact_placeholder")]
fn py_has_contact_placeholder(text: &str) -> bool {
    redact::has_contact_placeholder(text)
}

/// Canonical JSON (RFC 8785).
#[pyfunction]
#[pyo3(name = "canonical_json")]
fn py_canonical_json(input: &str) -> PyResult<String> {
    canonical::canonical_json(input)
        .map_err(|e| pyo3::exceptions::PyValueError::new_err(format!("invalid JSON: {e}")))
}

/// Finds which candidates reached the prompt.
///
/// `candidates` is a list of `(id, text)` pairs and `messages` the message
/// contents of the request in order. Each result is a dict with `id`, `match`,
/// `coverage` and `spans`, where a span is `(message_index, start, end)` in
/// code points of the original message.
#[pyfunction]
#[pyo3(name = "match_in_context")]
fn py_match_in_context<'py>(
    py: Python<'py>,
    candidates: Vec<(String, String)>,
    messages: Vec<String>,
) -> PyResult<Bound<'py, PyList>> {
    let matches = py.detach(|| matching::match_in_context(&candidates, &messages));
    let list = PyList::empty(py);
    for m in matches {
        let d = PyDict::new(py);
        d.set_item("id", m.id)?;
        d.set_item("match", match_name(m.match_kind))?;
        d.set_item("coverage", m.coverage)?;
        d.set_item("spans", m.spans)?;
        list.append(d)?;
    }
    Ok(list)
}

/// Finds which candidates supplied the values in a tool call's arguments.
///
/// Each result is a dict with `id`, `arg_path` and `match`.
#[pyfunction]
#[pyo3(name = "match_value_flow")]
fn py_match_value_flow<'py>(
    py: Python<'py>,
    args_json: &str,
    candidates: Vec<(String, String)>,
) -> PyResult<Bound<'py, PyList>> {
    let flows = py.detach(|| matching::match_value_flow(args_json, &candidates));
    let list = PyList::empty(py);
    for f in flows {
        let d = PyDict::new(py);
        d.set_item("id", f.id)?;
        d.set_item("arg_path", f.arg_path)?;
        d.set_item("match", match_name(f.match_kind))?;
        list.append(d)?;
    }
    Ok(list)
}

fn match_name(m: Match) -> &'static str {
    match m {
        Match::Exact => "exact",
        Match::Partial => "partial",
    }
}

#[pymodule]
#[pyo3(name = "_core")]
fn totem_core(m: &Bound<'_, PyModule>) -> PyResult<()> {
    m.add("__doc__", "Rust core of the Totem SDK.")?;
    m.add("PARTIAL_THRESHOLD", PARTIAL_THRESHOLD_EXPORT)?;
    m.add_function(wrap_pyfunction!(py_normalize, m)?)?;
    m.add_function(wrap_pyfunction!(py_hmac, m)?)?;
    m.add_function(wrap_pyfunction!(py_fingerprint, m)?)?;
    m.add_function(wrap_pyfunction!(py_redact, m)?)?;
    m.add_function(wrap_pyfunction!(py_has_contact_placeholder, m)?)?;
    m.add_function(wrap_pyfunction!(py_canonical_json, m)?)?;
    m.add_function(wrap_pyfunction!(py_match_in_context, m)?)?;
    m.add_function(wrap_pyfunction!(py_match_value_flow, m)?)?;
    Ok(())
}
