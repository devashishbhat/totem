//! Canonical JSON (RFC 8785), so that a JSON value has one fingerprint
//! whatever order its keys arrived in.

use serde_json::Value;

/// Renders a JSON string in canonical form: keys sorted by UTF-16 code unit,
/// no insignificant whitespace, numbers in their shortest round-tripping form.
pub fn canonical_json(input: &str) -> Result<String, String> {
    let value: Value = serde_json::from_str(input).map_err(|e| e.to_string())?;
    let mut out = String::with_capacity(input.len());
    write_value(&value, &mut out);
    Ok(out)
}

fn write_value(value: &Value, out: &mut String) {
    match value {
        Value::Null => out.push_str("null"),
        Value::Bool(b) => out.push_str(if *b { "true" } else { "false" }),
        Value::Number(n) => out.push_str(&format_number(n.as_f64().unwrap_or(0.0))),
        Value::String(s) => out.push_str(&escape(s)),
        Value::Array(items) => {
            out.push('[');
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_value(item, out);
            }
            out.push(']');
        }
        Value::Object(map) => {
            let mut keys: Vec<&String> = map.keys().collect();
            keys.sort_by_key(|k| utf16_units(k));
            out.push('{');
            for (i, key) in keys.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                out.push_str(&escape(key));
                out.push(':');
                write_value(&map[*key], out);
            }
            out.push('}');
        }
    }
}

/// Formats a number the way ECMAScript's `Number::toString` does, which is
/// what RFC 8785 requires.
fn format_number(f: f64) -> String {
    if f.fract() == 0.0 && f.abs() < 1e21 {
        return format!("{}", f as i64);
    }
    let mut s = format!("{f}");
    if s.contains('e') && !s.contains("e-") {
        s = s.replace('e', "e+");
    }
    s
}

fn escape(s: &str) -> String {
    serde_json::to_string(s).unwrap_or_else(|_| "\"\"".to_string())
}

fn utf16_units(s: &str) -> Vec<u16> {
    s.encode_utf16().collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sorts_keys_and_drops_whitespace() {
        let got = canonical_json(r#"{ "b": 1, "a": [2, 3] }"#).unwrap();
        assert_eq!(got, r#"{"a":[2,3],"b":1}"#);
    }

    #[test]
    fn integral_floats_lose_their_decimal_point() {
        assert_eq!(canonical_json("[1.0, 2.5]").unwrap(), "[1,2.5]");
    }

    #[test]
    fn rejects_broken_json() {
        assert!(canonical_json("{").is_err());
    }
}
