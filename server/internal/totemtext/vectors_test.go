package totemtext

import (
	"encoding/json"
	"os"
	"testing"
)

// The SDK fingerprints a memory in Rust and the server has to recognise the
// same memory from the same text. These vectors are generated from the Rust
// core (see testdata/README.md) and pin the two implementations together: if
// either drifts, this test fails rather than a memory silently splitting into
// two.
type vectors struct {
	Salt string `json:"salt"`
	Text []struct {
		Input                 string   `json:"input"`
		Normalized            string   `json:"normalized"`
		HMAC                  string   `json:"hmac"`
		Fingerprint           string   `json:"fingerprint"`
		Redacted              string   `json:"redacted"`
		Findings              []string `json:"findings"`
		HasContactPlaceholder bool     `json:"has_contact_placeholder"`
	} `json:"text"`
	JSON []struct {
		Input     string `json:"input"`
		Canonical string `json:"canonical"`
	} `json:"json"`
}

func TestMatchesTheRustCore(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	if len(v.Text) == 0 || len(v.JSON) == 0 {
		t.Fatal("vectors.json is empty")
	}

	for _, c := range v.Text {
		t.Run("text/"+short(c.Input), func(t *testing.T) {
			if got := Normalize(c.Input); got != c.Normalized {
				t.Errorf("Normalize = %q, Rust says %q", got, c.Normalized)
			}
			if got := HMAC(v.Salt, c.Input); got != c.HMAC {
				t.Errorf("HMAC = %q, Rust says %q", got, c.HMAC)
			}
			if got := Fingerprint(v.Salt, c.Input); got != c.Fingerprint {
				t.Errorf("Fingerprint = %q, Rust says %q", got, c.Fingerprint)
			}
			redacted, findings := Redact(v.Salt, c.Input)
			if redacted != c.Redacted {
				t.Errorf("Redact = %q, Rust says %q", redacted, c.Redacted)
			}
			if len(findings) != len(c.Findings) {
				t.Fatalf("found %d values, Rust found %d: %v vs %v",
					len(findings), len(c.Findings), findings, c.Findings)
			}
			for i, f := range findings {
				if string(f.Kind) != c.Findings[i] {
					t.Errorf("finding %d is %s, Rust says %s", i, f.Kind, c.Findings[i])
				}
			}
			if got := HasContactPlaceholder(redacted); got != c.HasContactPlaceholder {
				t.Errorf("HasContactPlaceholder = %v, Rust says %v", got, c.HasContactPlaceholder)
			}
		})
	}

	for _, c := range v.JSON {
		t.Run("json/"+short(c.Input), func(t *testing.T) {
			got, err := Canonical([]byte(c.Input))
			if err != nil {
				t.Fatalf("Canonical: %v", err)
			}
			if got != c.Canonical {
				t.Errorf("Canonical = %q, Rust says %q", got, c.Canonical)
			}
		})
	}
}

func short(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	if s == "" {
		return "empty"
	}
	return s
}
