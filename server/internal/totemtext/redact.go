package totemtext

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Kind names the class of a redacted value. It appears in the placeholder.
type Kind string

const (
	KindEmail       Kind = "EMAIL"
	KindPhone       Kind = "PHONE"
	KindCard        Kind = "CARD"
	KindKey         Kind = "KEY"
	KindJWT         Kind = "JWT"
	KindToken       Kind = "TOKEN"
	kindPlaceholder Kind = "PLACEHOLDER"
)

// Finding records one redacted value.
type Finding struct {
	Kind        Kind   `json:"kind"`
	Placeholder string `json:"placeholder"`
	Start       int    `json:"start"` // byte offset in the input
	End         int    `json:"end"`
}

// entropyFloor is the minimum Shannon entropy in bits per character for a long
// opaque string to count as a secret token.
const entropyFloor = 3.5

var (
	rePlaceholder = regexp.MustCompile(`\[(?:EMAIL|PHONE|CARD|KEY|JWT|TOKEN):[0-9a-f]{6}\]`)
	reJWT         = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	reKey         = regexp.MustCompile(`(?:sk-(?:proj-)?[A-Za-z0-9_-]{16,}` +
		`|AKIA[0-9A-Z]{16}` +
		`|gh[pousr]_[A-Za-z0-9]{20,}` +
		`|xox[baprs]-[A-Za-z0-9-]{10,}` +
		`|AIza[A-Za-z0-9_-]{30,})`)
	reEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*\.[A-Za-z]{2,}`)
	reCard  = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)
	rePhone = regexp.MustCompile(`(?:\+\d{1,3}[ .-]?)?(?:\(\d{3}\)|\d{3})[ .-]\d{3}[ .-]\d{4}\b`)
	reToken = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)
)

// rule is one detector. Lower order wins when two matches overlap.
type rule struct {
	kind  Kind
	order int
	re    *regexp.Regexp
	// accept rejects a regex hit that is not really a secret.
	accept func(string) bool
}

var rules = []rule{
	{kindPlaceholder, 0, rePlaceholder, nil},
	{KindJWT, 1, reJWT, nil},
	{KindKey, 2, reKey, nil},
	{KindEmail, 3, reEmail, nil},
	{KindCard, 4, reCard, isLuhn},
	{KindPhone, 5, rePhone, nil},
	{KindToken, 6, reToken, hasSecretEntropy},
}

type hit struct {
	kind  Kind
	order int
	start int
	end   int
}

// Redact replaces every secret-looking value with a typed, stable placeholder
// of the form [EMAIL:3f9a1c], where the suffix is the first six hex characters
// of the value's HMAC under the project salt. The same value therefore always
// yields the same placeholder, which keeps value flow visible without storing
// the value. Redact is idempotent: existing placeholders are left alone.
func Redact(salt, s string) (string, []Finding) {
	var hits []hit
	for _, r := range rules {
		for _, m := range r.re.FindAllStringIndex(s, -1) {
			if r.accept != nil && !r.accept(s[m[0]:m[1]]) {
				continue
			}
			hits = append(hits, hit{r.kind, r.order, m[0], m[1]})
		}
	}
	// Highest priority first, then leftmost, then longest, so the outcome does
	// not depend on the order the detectors ran in.
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.order != b.order {
			return a.order < b.order
		}
		if a.start != b.start {
			return a.start < b.start
		}
		return a.end > b.end
	})
	var kept []hit
	for _, h := range hits {
		overlaps := false
		for _, k := range kept {
			if h.start < k.end && k.start < h.end {
				overlaps = true
				break
			}
		}
		if !overlaps {
			kept = append(kept, h)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].start < kept[j].start })

	var b strings.Builder
	b.Grow(len(s))
	findings := make([]Finding, 0, len(kept))
	last := 0
	for _, h := range kept {
		b.WriteString(s[last:h.start])
		if h.kind == kindPlaceholder {
			b.WriteString(s[h.start:h.end])
		} else {
			p := Placeholder(salt, h.kind, s[h.start:h.end])
			b.WriteString(p)
			findings = append(findings, Finding{Kind: h.kind, Placeholder: p, Start: h.start, End: h.end})
		}
		last = h.end
	}
	b.WriteString(s[last:])
	return b.String(), findings
}

// Placeholder builds the stable placeholder for one value.
func Placeholder(salt string, kind Kind, value string) string {
	return fmt.Sprintf("[%s:%s]", kind, HMAC(salt, value)[:6])
}

// HasContactPlaceholder reports whether the text carries a redacted contact
// detail or a bare URL, which is what makes an untrusted memory worth an alert.
func HasContactPlaceholder(s string) bool {
	return strings.Contains(s, "[EMAIL:") || strings.Contains(s, "[PHONE:") ||
		strings.Contains(s, "http://") || strings.Contains(s, "https://")
}

func isLuhn(s string) bool {
	digits := make([]int, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, int(r-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := digits[i]
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

func hasSecretEntropy(s string) bool {
	var counts [256]float64
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	n := float64(len(s))
	e := 0.0
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := c / n
		e -= p * math.Log2(p)
	}
	return e >= entropyFloor
}
