package equivalence_test

import (
	"testing"

	"github.com/HelixDevelopment/helix_perf_cache/pkg/backend"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/equivalence"
)

// turns builds a multi-turn conversation sharing a stable prefix.
func turns() []backend.Turn {
	prefix := make([]int, 100)
	for i := range prefix {
		prefix[i] = i + 1
	}
	return []backend.Turn{{Prefix: prefix, New: []int{9001, 9002, 9003, 9004}}}
}

// TestCorrectCacheIsEquivalent: a correct backend's cached output equals its
// from-scratch recompute, token-for-token (the KV-cache invariant).
func TestCorrectCacheIsEquivalent(t *testing.T) {
	b := backend.NewCorrectStub("correct")
	on, err := b.Complete(turns(), true)
	if err != nil {
		t.Fatalf("cache-on: %v", err)
	}
	off, err := b.Complete(turns(), false)
	if err != nil {
		t.Fatalf("cache-off: %v", err)
	}
	v := equivalence.Compare(on.Tokens, off.Tokens)
	if !v.Equivalent {
		t.Fatalf("correct cache MUST be equivalent, got: %s (diverged at %d)", v.Reason, v.DivergedAt)
	}
}

// TestCorruptCacheDivergenceIsCaught: the oracle MUST catch a buggy/stale cache
// whose cached output differs from the recompute. This is the anti-tautology
// proof — the oracle genuinely fails on a real divergence, so its PASS above is
// meaningful (RISKS.md R-CORRECTNESS).
func TestCorruptCacheDivergenceIsCaught(t *testing.T) {
	b := backend.NewCorruptStub("corrupt")
	on, _ := b.Complete(turns(), true)   // corrupted (cache-on)
	off, _ := b.Complete(turns(), false) // clean recompute
	v := equivalence.Compare(on.Tokens, off.Tokens)
	if v.Equivalent {
		t.Fatal("corrupt cache MUST NOT be reported equivalent — the oracle is blind (a bluff)")
	}
	if v.DivergedAt < 0 {
		t.Fatalf("divergence index MUST be reported, got %d", v.DivergedAt)
	}
}

func TestTokenCountMismatch(t *testing.T) {
	v := equivalence.Compare([]int{1, 2, 3}, []int{1, 2})
	if v.Equivalent {
		t.Fatal("different-length sequences MUST NOT be equivalent")
	}
}
