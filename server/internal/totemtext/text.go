// Package totemtext holds the text primitives that must behave identically in
// the Rust SDK core and in the Go server: normalization, fingerprinting and
// redaction. The rules are specified once in docs/text-rules.md.
package totemtext

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize applies NFKC, trims, and collapses every run of whitespace to a
// single space.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	space := false
	started := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && started {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
		space = false
		started = true
	}
	return b.String()
}

// HMAC is the hex HMAC-SHA256 of the normalized text under the project salt.
func HMAC(salt, s string) string {
	m := hmac.New(sha256.New, []byte(salt))
	m.Write([]byte(Normalize(s)))
	return hex.EncodeToString(m.Sum(nil))
}

// Fingerprint is the identity of a piece of observed text: the HMAC of its
// redacted form. The SDK and the server compute the same value because
// redaction is idempotent.
func Fingerprint(salt, s string) string {
	red, _ := Redact(salt, s)
	return HMAC(salt, red)
}
