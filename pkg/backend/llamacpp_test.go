package backend

import (
	"os"
	"testing"
	"time"
)

// liveEndpoint returns the llama.cpp endpoint to test against. Defaults to the
// conventional local port; a consuming host overrides via HELIX_PERF_LLAMACPP.
// The test SKIPs (never fake-PASSes, §11.4.69) when the server is absent.
func liveEndpoint() string {
	if e := os.Getenv("HELIX_PERF_LLAMACPP"); e != "" {
		return e
	}
	return "http://localhost:18434"
}

// TestLlamaCppLive_RealPrefixReuse is the REAL Track-A integration test. It
// proves — against a live server — that the equivalence comparison is NOT the
// tautology the stub-guide used: the cache-on and cache-off runs execute TWO
// DISTINCT server paths (cache-on reuses a warmed prefix's KV, cache-off
// reprocesses the whole prompt), and a correct KV-prefix cache changes ONLY
// timing, so the produced tokens are identical.
//
// Honest SKIP (§11.4.3/§11.4.69) when no server is reachable.
func TestLlamaCppLive_RealPrefixReuse(t *testing.T) {
	ep := liveEndpoint()
	b := NewLlamaCpp("llamacpp-test", ep, 24)
	if !b.Available() {
		t.Skipf("SKIP (network_unreachable_external): no live llama.cpp /health OK at %s", ep)
	}

	prefixText := ""
	for i := 0; i < 120; i++ {
		prefixText += "You are a senior software engineer assisting with a large codebase. Follow the coding standards strictly. "
	}
	prefixToks, err := b.Tokenize(prefixText)
	if err != nil {
		t.Fatalf("tokenize prefix: %v", err)
	}
	if len(prefixToks) < 500 {
		t.Fatalf("prefix too short to exercise prefix reuse: %d tokens", len(prefixToks))
	}
	newToks, err := b.Tokenize(" Summarize the single most important rule in one sentence:")
	if err != nil {
		t.Fatalf("tokenize new: %v", err)
	}
	turns := []Turn{{Prefix: prefixToks, New: newToks}}

	off, err := b.Complete(turns, false)
	if err != nil {
		t.Fatalf("cache-off Complete: %v", err)
	}
	on, err := b.Complete(turns, true)
	if err != nil {
		t.Fatalf("cache-on Complete: %v", err)
	}

	// DISTINCT PATHS: cache-off reprocesses the whole prompt (no reuse), cache-on
	// reuses the warmed prefix's KV. If both reported the same reuse the
	// comparison would be tautological — this asserts they are genuinely different.
	if off.PrefixReused != 0 {
		t.Errorf("cache-off should reuse ZERO prefix tokens, got %d (path not distinct)", off.PrefixReused)
	}
	if on.PrefixReused < len(prefixToks)/2 {
		t.Errorf("cache-on should reuse the warmed prefix (~%d tokens), got %d — prefix reuse not exercised", len(prefixToks), on.PrefixReused)
	}
	if on.PrefillTokens >= off.PrefillTokens {
		t.Errorf("cache-on prefill (%d) should be far less than cache-off prefill (%d) — no prefill skip measured", on.PrefillTokens, off.PrefillTokens)
	}

	// EQUIVALENCE: a correct KV-prefix cache changes only timing, not output.
	if len(on.Tokens) != len(off.Tokens) {
		t.Fatalf("token-count mismatch: cache-on %d vs cache-off %d", len(on.Tokens), len(off.Tokens))
	}
	for i := range on.Tokens {
		if on.Tokens[i] != off.Tokens[i] {
			t.Fatalf("token divergence at %d: cache-on %d vs cache-off %d (a correct KV cache must not change output)", i, on.Tokens[i], off.Tokens[i])
		}
	}

	// The prefill-skip must be a real, positive timing win (the KV-cache's effect).
	if on.PrefillDur >= off.PrefillDur {
		t.Errorf("cache-on prefill duration (%v) should be less than cache-off (%v)", on.PrefillDur, off.PrefillDur)
	}
	t.Logf("REAL prefix reuse: off prefill %v/%d tok, on prefill %v/%d tok (reused %d); tokens identical (%d)",
		off.PrefillDur, off.PrefillTokens, on.PrefillDur, on.PrefillTokens, on.PrefixReused, len(on.Tokens))
}

// TestToResult_MapsServerTimings unit-tests the pure mapping from a server
// response to a CompletionResult — no server required.
func TestToResult_MapsServerTimings(t *testing.T) {
	var cr completionResp
	cr.Tokens = []int{1, 2, 3}
	cr.Timings.PromptN = 12
	cr.Timings.CacheN = 2281
	cr.Timings.PromptMs = 16.0
	cr.Timings.PredictedN = 3
	cr.Timings.PredictedMs = 30.0

	r := toResult("b", cr, true)
	if r.PrefillTokens != 12 || r.PrefixReused != 2281 || r.DecodeTokens != 3 {
		t.Fatalf("token accounting wrong: %+v", r)
	}
	if r.PrefillDur != 16*time.Millisecond {
		t.Errorf("prefill dur mapping wrong: %v", r.PrefillDur)
	}
	if r.DecodeDur != 30*time.Millisecond {
		t.Errorf("decode dur mapping wrong: %v", r.DecodeDur)
	}
	if !r.CacheOn || len(r.Tokens) != 3 {
		t.Errorf("cacheOn/tokens mapping wrong: %+v", r)
	}
}

// TestLlamaCppUnavailable_SkipsHonestly proves the Available() probe returns
// false for a dead endpoint (so the harness SKIPs, never fake-PASSes).
func TestLlamaCppUnavailable_SkipsHonestly(t *testing.T) {
	b := NewLlamaCpp("dead", "http://127.0.0.1:1", 8)
	if b.Available() {
		t.Fatal("Available() must be false for an unreachable endpoint")
	}
}
