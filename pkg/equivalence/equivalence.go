// Package equivalence is the CORRECTNESS oracle: it compares a cached
// multi-turn output against a from-scratch full-context recompute of the SAME
// turn, token-for-token.
//
// This DELIBERATELY replaces the pasted guide's bluff test, which asserted
//
//	text_no_cache == text_with_cache
//
// on two IDENTICALLY-computed values — a tautology that always passes and proves
// nothing (§11.4.6). Here the two inputs come from TWO DISTINCT execution paths
// (cache-on reuse vs cache-off recompute); their agreement is a real property of
// the backend, and their divergence is a release-blocking correctness defect
// (RISKS.md R-CORRECTNESS).
package equivalence

// Verdict is the outcome of a token-for-token equivalence check.
type Verdict struct {
	Equivalent bool   `json:"equivalent"`
	Reason     string `json:"reason"`
	DivergedAt int    `json:"diverged_at"` // index of first divergence, -1 if none
}

// Compare returns Equivalent iff the two token sequences are identical.
//
// Tolerance note (§11.4.6): real GPU non-determinism may justify a small,
// DECLARED, CAPTURED numerical tolerance. That tolerance is a Phase-3 decision
// measured on the target GPU, never guessed; this Phase-0-1 oracle asserts exact
// token-ID identity (the correct default for a deterministic backend).
func Compare(cacheOn, cacheOff []int) Verdict {
	if len(cacheOn) != len(cacheOff) {
		return Verdict{Equivalent: false, Reason: "token-count mismatch", DivergedAt: minInt(len(cacheOn), len(cacheOff))}
	}
	for i := range cacheOn {
		if cacheOn[i] != cacheOff[i] {
			return Verdict{Equivalent: false, Reason: "token divergence (cached output != from-scratch recompute)", DivergedAt: i}
		}
	}
	return Verdict{Equivalent: true, Reason: "token-for-token identical", DivergedAt: -1}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
