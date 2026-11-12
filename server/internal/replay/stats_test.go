package replay

import (
	"math"
	"testing"

	"github.com/devashishbhat/totem/server/internal/store"
)

func TestWilson(t *testing.T) {
	cases := []struct {
		k, n           int
		wantLo, wantHi float64
	}{
		// Known values for a 95% Wilson score interval.
		{0, 8, 0.0, 0.324},
		{8, 8, 0.676, 1.0},
		{4, 8, 0.215, 0.785},
		{0, 0, 0.0, 1.0},
	}
	for _, c := range cases {
		lo, hi := wilson(c.k, c.n)
		if math.Abs(lo-c.wantLo) > 0.005 || math.Abs(hi-c.wantHi) > 0.005 {
			t.Errorf("wilson(%d, %d) = (%.3f, %.3f), want (%.3f, %.3f)",
				c.k, c.n, lo, hi, c.wantLo, c.wantHi)
		}
	}
}

func TestNewcombeDifferenceAndVerdict(t *testing.T) {
	cases := []struct {
		name                                     string
		baseFlips, baseN, variantFlips, variantN int
		wantEffect                               float64
		wantVerdict                              string
	}{
		{
			name:      "the decision changes every time",
			baseFlips: 0, baseN: 8, variantFlips: 8, variantN: 8,
			wantEffect: 1.0, wantVerdict: store.VerdictStrong,
		},
		{
			name:      "the decision changes most of the time",
			baseFlips: 0, baseN: 8, variantFlips: 5, variantN: 8,
			wantEffect: 0.625, wantVerdict: store.VerdictStrong,
		},
		{
			name:      "a quarter of the time, with a clear interval",
			baseFlips: 0, baseN: 16, variantFlips: 5, variantN: 16,
			wantEffect: 0.3125, wantVerdict: store.VerdictModerate,
		},
		{
			name:      "nothing changes",
			baseFlips: 0, baseN: 16, variantFlips: 0, variantN: 16,
			wantEffect: 0.0, wantVerdict: store.VerdictNone,
		},
		{
			name:      "the agent was already unstable",
			baseFlips: 4, baseN: 8, variantFlips: 5, variantN: 8,
			wantEffect: 0.125, wantVerdict: store.VerdictInconclusive,
		},
		{
			name:      "one sample each proves nothing",
			baseFlips: 0, baseN: 1, variantFlips: 1, variantN: 1,
			wantEffect: 1.0, wantVerdict: store.VerdictInconclusive,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := NewcombeDifference(c.baseFlips, c.baseN, c.variantFlips, c.variantN)
			if math.Abs(d.Effect-c.wantEffect) > 0.001 {
				t.Errorf("effect = %.3f, want %.3f", d.Effect, c.wantEffect)
			}
			if got := Verdict(d); got != c.wantVerdict {
				t.Errorf("verdict = %s, want %s (effect %.2f, CI %.2f to %.2f)",
					got, c.wantVerdict, d.Effect, d.CILow, d.CIHigh)
			}
			if d.CILow > d.Effect || d.CIHigh < d.Effect {
				t.Errorf("the interval (%.3f, %.3f) excludes the effect %.3f",
					d.CILow, d.CIHigh, d.Effect)
			}
			if d.CILow < -1 || d.CIHigh > 1 {
				t.Errorf("the interval (%.3f, %.3f) leaves [-1, 1]", d.CILow, d.CIHigh)
			}
		})
	}
}

func TestMoreSamplesMeanAStrongerLowerBound(t *testing.T) {
	// The same proportions from more samples must be at least as convincing.
	small := NewcombeDifference(0, 2, 2, 2)
	large := NewcombeDifference(0, 8, 8, 8)
	if large.CILow <= small.CILow {
		t.Fatalf("eight samples gave lower bound %.3f, two gave %.3f",
			large.CILow, small.CILow)
	}
	if Verdict(large) != store.VerdictStrong {
		t.Fatalf("eight of eight should be strong, got %s", Verdict(large))
	}
}

func TestOneSamplePerArmIsNeverStrong(t *testing.T) {
	d := NewcombeDifference(0, 1, 1, 1)
	if got := Verdict(d); got != store.VerdictInconclusive {
		t.Fatalf("one sample per arm gave %s: %+v", got, d)
	}
}
