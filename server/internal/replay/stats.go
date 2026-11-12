package replay

import (
	"math"

	"github.com/devashishbhat/totem/server/internal/store"
)

// z is the 97.5th percentile of the standard normal, for a 95% interval.
const z = 1.959963984540054

// wilson returns the Wilson score interval for k successes out of n.
func wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 1
	}
	fn := float64(n)
	p := float64(k) / fn
	denom := 1 + z*z/fn
	center := (p + z*z/(2*fn)) / denom
	half := (z / denom) * math.Sqrt(p*(1-p)/fn+z*z/(4*fn*fn))
	return clamp(center-half, 0, 1), clamp(center+half, 0, 1)
}

// Difference is the effect of removing a memory, with its interval.
type Difference struct {
	BaselineFlip float64
	VariantFlip  float64
	Effect       float64
	CILow        float64
	CIHigh       float64
}

// NewcombeDifference compares the two arms. baselineFlips of baselineN samples
// differed from the reference output without the edit; variantFlips of variantN
// differed with the memory removed. The interval is Newcombe's hybrid score
// interval for the difference of two proportions, which behaves at the 0 and 1
// boundaries that a deterministic agent produces.
func NewcombeDifference(baselineFlips, baselineN, variantFlips, variantN int) Difference {
	pb := proportion(baselineFlips, baselineN)
	pv := proportion(variantFlips, variantN)
	lb, ub := wilson(baselineFlips, baselineN)
	lv, uv := wilson(variantFlips, variantN)
	d := pv - pb
	lo := d - math.Sqrt(sq(pv-lv)+sq(ub-pb))
	hi := d + math.Sqrt(sq(uv-pv)+sq(pb-lb))
	return Difference{
		BaselineFlip: pb,
		VariantFlip:  pv,
		Effect:       d,
		CILow:        clamp(lo, -1, 1),
		CIHigh:       clamp(hi, -1, 1),
	}
}

// Verdict grades the evidence.
func Verdict(d Difference) string {
	switch {
	case d.Effect >= 0.5 && d.CILow > 0:
		return store.VerdictStrong
	case d.Effect >= 0.2 && d.CILow > 0:
		return store.VerdictModerate
	case d.CIHigh < 0.2:
		return store.VerdictNone
	default:
		return store.VerdictInconclusive
	}
}

func proportion(k, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(k) / float64(n)
}

func sq(x float64) float64 { return x * x }

func clamp(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}
