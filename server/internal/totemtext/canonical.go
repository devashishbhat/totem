package totemtext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Canonical renders JSON in the canonical form of RFC 8785: object keys sorted
// by their UTF-16 code units, no insignificant whitespace, and numbers in the
// shortest round-tripping form. Two values that mean the same thing therefore
// produce the same string, which is what makes fingerprints and output
// comparisons stable.
func Canonical(raw []byte) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("canonicalize: %w", err)
	}
	var b bytes.Buffer
	if err := writeCanonical(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeCanonical(b *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(val))
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return fmt.Errorf("canonicalize number %q: %w", val.String(), err)
		}
		b.WriteString(formatNumber(f))
	case float64:
		b.WriteString(formatNumber(val))
	case string:
		raw, err := json.Marshal(val)
		if err != nil {
			return err
		}
		b.Write(raw)
	case []any:
		b.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			raw, err := json.Marshal(k)
			if err != nil {
				return err
			}
			b.Write(raw)
			b.WriteByte(':')
			if err := writeCanonical(b, val[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("canonicalize: unsupported value %T", v)
	}
	return nil
}

// formatNumber writes a number the way ECMAScript's Number::toString does,
// which is what RFC 8785 requires.
func formatNumber(f float64) string {
	if f == float64(int64(f)) && f < 1e21 && f > -1e21 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// lessUTF16 compares two strings by their UTF-16 code units.
func lessUTF16(a, b string) bool {
	ar, br := utf16Units(a), utf16Units(b)
	for i := 0; i < len(ar) && i < len(br); i++ {
		if ar[i] != br[i] {
			return ar[i] < br[i]
		}
	}
	return len(ar) < len(br)
}

func utf16Units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			continue
		}
		out = append(out, uint16(r))
	}
	return out
}
