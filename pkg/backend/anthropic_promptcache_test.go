package backend

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestAnthropicUnavailable_SkipsHonestly: with NO credential in the environment
// the backend reports Available()==false so the harness SKIPs with a reason,
// never a fake PASS (§11.4.69). Runs always — it is the credentials_absent path
// this session's host is actually in.
func TestAnthropicUnavailable_SkipsHonestly(t *testing.T) {
	// Neutralise any ambient credential for a deterministic assertion.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	b := NewAnthropicPromptCache("anthropic-test", "", 8)
	if b.Available() {
		t.Fatal("Available() MUST be false when no credential is present")
	}
	// Complete on an unavailable backend MUST error, never fake a result.
	if _, err := b.Complete([]Turn{{Prefix: textToTokens("hello"), New: textToTokens("world")}}, false); err == nil {
		t.Fatal("Complete on an unavailable backend MUST return an error, never a fabricated result")
	}
	if names := b.MissingCredentialEnvNames(); names == "" {
		t.Fatal("MissingCredentialEnvNames MUST name the absent env vars for an actionable SKIP")
	}
}

// TestAnthropicAvailable_WithCredential: a present credential flips Available()
// to true (the honest gate). Runs always with a synthetic key — Available()
// makes NO network call, so this is side-effect-free and never spends money.
func TestAnthropicAvailable_WithCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-not-a-real-key")
	b := NewAnthropicPromptCache("anthropic-test", "", 8)
	if !b.Available() {
		t.Fatal("Available() MUST be true when a credential env var is set")
	}
}

// TestToPromptCacheResult_MapsUsage unit-tests the pure usage->CompletionResult
// mapping — no network. It proves PrefixReused comes from the provider's
// cache_read_input_tokens (the REAL prompt-cache read signal), PrefillTokens
// from input_tokens, latency is carried verbatim, and the response TEXT is
// carried as the correctness Tokens.
func TestToPromptCacheResult_MapsUsage(t *testing.T) {
	var mr anthropicMessageResp
	mr.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: "Cache the stable prefix."}}
	mr.Usage.InputTokens = 11
	mr.Usage.OutputTokens = 6
	mr.Usage.CacheCreationInputTokens = 0
	mr.Usage.CacheReadInputTokens = 2560 // a real prompt-cache read

	r := toPromptCacheResult("anthropic", mr, true, 137*time.Millisecond)
	if r.PrefixReused != 2560 {
		t.Fatalf("PrefixReused MUST equal cache_read_input_tokens (2560), got %d", r.PrefixReused)
	}
	if r.PrefillTokens != 11 {
		t.Fatalf("PrefillTokens MUST equal input_tokens (11), got %d", r.PrefillTokens)
	}
	if r.DecodeTokens != 6 {
		t.Fatalf("DecodeTokens MUST equal output_tokens (6), got %d", r.DecodeTokens)
	}
	if r.PrefillDur != 137*time.Millisecond {
		t.Fatalf("PrefillDur MUST carry the measured latency, got %v", r.PrefillDur)
	}
	if r.DecodeDur != 0 {
		t.Fatalf("Track B has no server decode split; DecodeDur MUST be 0, got %v", r.DecodeDur)
	}
	if !r.CacheOn || r.Backend != "anthropic" {
		t.Fatalf("cacheOn/backend mapping wrong: %+v", r)
	}
	if got := intsToString(r.Tokens); got != "Cache the stable prefix." {
		t.Fatalf("correctness Tokens MUST carry the response text; got %q", got)
	}
}

// TestTextCodepointBridge_RoundTrips proves the text<->[]int codepoint bridge is
// faithful (so the equivalence oracle compares real content) and DISCRIMINATING
// (a one-character change diverges — never a tautology).
func TestTextCodepointBridge_RoundTrips(t *testing.T) {
	for _, s := range []string{"", "ok", "Follow the coding standards — strictly. 日本語 🚀"} {
		if got := intsToString(textToTokens(s)); got != s {
			t.Fatalf("round-trip lost content: %q -> %q", s, got)
		}
	}
	a := textToTokens("the answer is 42")
	b := textToTokens("the answer is 43") // one-char divergence
	if len(a) != len(b) {
		t.Fatalf("expected same length, got %d vs %d", len(a), len(b))
	}
	diverged := false
	for i := range a {
		if a[i] != b[i] {
			diverged = true
		}
	}
	if !diverged {
		t.Fatal("codepoint encoding MUST discriminate divergent content (equivalence would be a tautology otherwise)")
	}
}

// TestAnthropicBenchNote_HonestBoundary asserts the Track-B note keeps the
// §11.4.112 boundary explicit and never leaks the stub note.
func TestAnthropicBenchNote_HonestBoundary(t *testing.T) {
	n := NewAnthropicPromptCache("anthropic-test", "", 8).BenchNote()
	for _, must := range []string{"PROMPT-CACHE", "NOT a decode speedup", "§11.4.112"} {
		if !strings.Contains(n, must) {
			t.Fatalf("Track-B note MUST contain %q (honest boundary); note=%q", must, n)
		}
	}
	// Must not read like the stub note (whose markers are "STUB" /
	// "DeterministicStub"). "NO value is modelled" is the honest opposite and is
	// legitimately present — do NOT forbid the word "modelled" itself.
	if strings.Contains(n, "STUB") || strings.Contains(n, "DeterministicStub") {
		t.Fatalf("Track-B note MUST NOT read like the stub note; note=%q", n)
	}
}

// anthropicLiveConfigured reports whether a real credential is present for the
// live integration test.
func anthropicLiveConfigured() bool {
	return os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != ""
}

// TestAnthropicLive_RealPromptCacheRead is the REAL Track-B integration test. It
// drives two DISTINCT server paths — cache-off (full input reprocessed) vs
// cache-on (warmed prefix reused) — and asserts the provider reports a genuine
// cache read on cache-on and none on cache-off, the response content is
// identical (a correct prompt-cache changes only cost/latency, never content),
// and the cache-on read is a real input-token-reuse win. It SKIPs honestly
// (§11.4.3/§11.4.69 credentials_absent) when no credential is configured — the
// state this session's host is in — never a fake PASS and never an autonomous
// paid call.
func TestAnthropicLive_RealPromptCacheRead(t *testing.T) {
	if !anthropicLiveConfigured() {
		t.Skip("SKIP (credentials_absent): ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN not set — the live Track-B prompt-cache measurement is MEASUREMENT PENDING. Set a credential (operator-gated: paid API call) and re-run.")
	}
	b := NewAnthropicPromptCache("anthropic-live", "", 24)
	if !b.Available() {
		t.Fatal("Available() must be true when a credential is set")
	}
	// A prefix large enough to exceed the prompt-cache minimum for any model.
	prefix := ""
	for i := 0; i < 400; i++ {
		prefix += "You are a senior software engineer assisting with a large codebase. Follow the coding standards strictly. "
	}
	turns := []Turn{{
		Prefix: StringToTurnInts(prefix),
		New:    StringToTurnInts("Summarize the single most important rule in one sentence."),
	}}

	off, err := b.Complete(turns, false)
	if err != nil {
		t.Fatalf("cache-off Complete: %v", err)
	}
	on, err := b.Complete(turns, true)
	if err != nil {
		t.Fatalf("cache-on Complete: %v", err)
	}
	if off.PrefixReused != 0 {
		t.Errorf("cache-off MUST report zero cache_read_input_tokens, got %d (path not distinct)", off.PrefixReused)
	}
	if on.PrefixReused <= 0 {
		t.Errorf("cache-on MUST report a real prompt-cache read (>0), got %d — prefix reuse not exercised", on.PrefixReused)
	}
	if len(on.Tokens) != len(off.Tokens) {
		t.Fatalf("content length mismatch cache-on %d vs cache-off %d (temperature=0 must be deterministic)", len(on.Tokens), len(off.Tokens))
	}
	for i := range on.Tokens {
		if on.Tokens[i] != off.Tokens[i] {
			t.Fatalf("content diverged at %d — a correct prompt-cache must not change output", i)
		}
	}
	t.Logf("REAL Track-B prompt-cache: cache-off read=%d input=%d latency=%v | cache-on read=%d input=%d latency=%v",
		off.PrefixReused, off.PrefillTokens, off.PrefillDur, on.PrefixReused, on.PrefillTokens, on.PrefillDur)
}
