package totemtext

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"collapses whitespace", "  a \n\t b  ", "a b"},
		{"empty", "", ""},
		{"whitespace only", " \n\t ", ""},
		{"NFKC folds a ligature", "ﬁx", "fix"},
		{"keeps case", "Acme", "Acme"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.in); got != c.want {
				t.Fatalf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	for _, in := range []string{"  a  b ", "ﬁx", "", "a b"} {
		once := Normalize(in)
		if twice := Normalize(once); twice != once {
			t.Fatalf("Normalize is not idempotent for %q: %q then %q", in, once, twice)
		}
	}
}

func TestHMAC(t *testing.T) {
	if HMAC("s", "a b") != HMAC("s", " a   b ") {
		t.Fatal("whitespace changed the fingerprint")
	}
	if HMAC("s", "a b") == HMAC("t", "a b") {
		t.Fatal("the salt did not change the fingerprint")
	}
	if len(HMAC("s", "a")) != 64 {
		t.Fatal("expected a 32-byte hex digest")
	}
}

func TestRedact(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		kind  Kind
		plain bool // the text should come back unchanged
	}{
		{name: "email", in: "write to billing@acme-payments.example now", kind: KindEmail},
		{name: "api key", in: "key sk-abcdefghijklmnopqrstuvwxyz0123", kind: KindKey},
		{
			name: "jwt",
			in: "token eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
				"eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
			kind: KindJWT,
		},
		{name: "card passing luhn", in: "card 4111 1111 1111 1111", kind: KindCard},
		{name: "phone", in: "call 415-555-0198 today", kind: KindPhone},
		{name: "prose", in: "Invoices for Acme are approved on Friday.", plain: true},
		{name: "a plain long number", in: "order 1234567890123456789", plain: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, findings := Redact("salt", c.in)
			if c.plain {
				if out != c.in || len(findings) != 0 {
					t.Fatalf("Redact(%q) = %q, %v; wanted it untouched", c.in, out, findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("Redact(%q) found %d values, want 1: %v", c.in, len(findings), findings)
			}
			if findings[0].Kind != c.kind {
				t.Fatalf("found kind %s, want %s", findings[0].Kind, c.kind)
			}
			if !strings.Contains(out, findings[0].Placeholder) {
				t.Fatalf("placeholder %q is not in %q", findings[0].Placeholder, out)
			}
		})
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	in := "mail billing@acme-payments.example, card 4111 1111 1111 1111, key sk-abcdefghijklmnopqrst"
	once, first := Redact("salt", in)
	twice, second := Redact("salt", once)
	if once != twice {
		t.Fatalf("second pass changed the text:\n%q\n%q", once, twice)
	}
	if len(second) != 0 {
		t.Fatalf("second pass found %v", second)
	}
	if len(first) != 3 {
		t.Fatalf("first pass found %d values, want 3: %v", len(first), first)
	}
}

func TestTheSameValueAlwaysGetsTheSamePlaceholder(t *testing.T) {
	// This is what lets the dashboard show a value flowing from a memory into
	// a tool argument without ever storing the value.
	memory, _ := Redact("salt", "Invoices for Acme go to billing@acme-payments.example")
	args, _ := Redact("salt", `{"to":"billing@acme-payments.example"}`)
	other, _ := Redact("salt", "ap@acme.example")
	placeholder := Placeholder("salt", KindEmail, "billing@acme-payments.example")
	if !strings.Contains(memory, placeholder) || !strings.Contains(args, placeholder) {
		t.Fatalf("placeholder %q missing from %q or %q", placeholder, memory, args)
	}
	if strings.Contains(other, placeholder) {
		t.Fatal("a different address produced the same placeholder")
	}
}

func TestFingerprintMatchesRedactedText(t *testing.T) {
	raw := "Invoices for Acme go to billing@acme-payments.example"
	redacted, _ := Redact("salt", raw)
	// The SDK sends the fingerprint of the raw text and the redacted content;
	// the server must be able to recompute the same fingerprint from what it
	// stored.
	if Fingerprint("salt", raw) != Fingerprint("salt", redacted) {
		t.Fatal("the server cannot recompute the SDK's fingerprint")
	}
	if Fingerprint("salt", raw) != HMAC("salt", redacted) {
		t.Fatal("the fingerprint is not the HMAC of the redacted text")
	}
}

func TestHasContactPlaceholder(t *testing.T) {
	cases := map[string]bool{
		"Invoices go to [EMAIL:3f9a1c]": true,
		"Call [PHONE:aabbcc]":           true,
		"See https://vendor.example":    true,
		"Acme pays on net 30":           false,
	}
	for in, want := range cases {
		if got := HasContactPlaceholder(in); got != want {
			t.Fatalf("HasContactPlaceholder(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{ "b": 1, "a": [2, 3] }`, `{"a":[2,3],"b":1}`},
		{`[1.0, 2.5]`, `[1,2.5]`},
		{`{"b":null,"a":true}`, `{"a":true,"b":null}`},
		{`{"é":1,"a":2}`, `{"a":2,"é":1}`},
		{"", ""},
	}
	for _, c := range cases {
		got, err := Canonical([]byte(c.in))
		if err != nil {
			t.Fatalf("Canonical(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("Canonical(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := Canonical([]byte("{")); err == nil {
		t.Fatal("expected an error for broken JSON")
	}
}
