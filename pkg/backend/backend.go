// Package backend defines the serving-path abstraction the benchmark harness
// drives, plus a DETERMINISTIC STUB used to self-validate the harness at the
// Phase 0-1 foundation stage (no real model weights are called yet).
//
// Anti-bluff note (§11.4.6): the stub is honest about being a stub. Its OUTPUT
// tokens are a pure deterministic function of the FULL input context — exactly
// as a real temperature=0 / fixed-seed model would behave — so the equivalence
// and speedup analyzers exercise REAL comparison logic against it. A KV-prefix
// cache is a PERFORMANCE optimization that MUST NOT change the produced tokens;
// the stub encodes that invariant (correct variant), and a deliberately-broken
// variant encodes its violation, so the analyzers' self-validation is genuine.
//
// What is STUB vs REAL at this phase:
//   - STUB : the model itself (genTokens hash-chain), the per-token latency
//     model (prefillPerTok / decodePerTok constants).
//   - REAL : the Turn / CompletionResult data contract, the cache-on vs
//     cache-off code paths (distinct prefill accounting), the
//     prefix-reuse bookkeeping, and every analyzer that consumes results.
//
// The real Track-A (llama.cpp) and Track-B (Anthropic / ccr-provider) backends
// implement this same interface and are wired in later phases (§11.4.197).
package backend

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

// Modelled per-token latency for the stub (STUB values, not measured).
const (
	prefillPerTok = 2 * time.Millisecond
	decodePerTok  = 5 * time.Millisecond
	genLen        = 8 // tokens generated per completion (STUB)
	vocabSize     = 32000
)

// Turn is one conversational turn: a stable Prefix (system + tools + prior
// context — the KV-reuse candidate) plus the New tokens of this turn.
type Turn struct {
	Prefix []int `json:"prefix"`
	New    []int `json:"new"`
}

// CompletionResult is the captured output + timing of one completion. It is the
// load-bearing evidence record every analyzer consumes (§11.4.5 / §11.4.69).
type CompletionResult struct {
	Tokens        []int         `json:"tokens"`         // generated token IDs
	PrefillTokens int           `json:"prefill_tokens"` // tokens that actually went through prefill
	PrefixReused  int           `json:"prefix_reused"`  // prefix tokens served from cache (prefill skipped)
	DecodeTokens  int           `json:"decode_tokens"`
	PrefillDur    time.Duration `json:"prefill_dur_ns"`
	DecodeDur     time.Duration `json:"decode_dur_ns"`
	CacheOn       bool          `json:"cache_on"`
	Backend       string        `json:"backend"`
}

// Backend abstracts a serving path (llama.cpp local = Track A;
// Anthropic / ccr-provider = Track B).
type Backend interface {
	Name() string
	Available() bool
	// Complete runs the given turns.
	//   cacheOn=false : from-scratch full-context recompute (prefill on everything).
	//   cacheOn=true  : reuse a matching prefix's KV where the backend supports it.
	// The RETURNED TOKENS MUST be identical for cacheOn and cacheOff on the same
	// turns for a correct backend — the cache only changes TIMING, never output.
	Complete(turns []Turn, cacheOn bool) (CompletionResult, error)
}

// ErrNoTurns is returned when Complete is called with an empty turn list.
var ErrNoTurns = errors.New("backend: no turns provided")

// DeterministicStub is a stub model whose output depends ONLY on the full input
// context. corrupt=true simulates a BUGGY/STALE cache: the cache-on path emits
// different tokens than the cache-off path (a correctness defect, RISKS.md
// R-CORRECTNESS). avail=false lets tests exercise the honest-skip path.
type DeterministicStub struct {
	name    string
	corrupt bool
	avail   bool
}

// NewCorrectStub returns a stub whose cache-on output equals its cache-off
// output (the correct KV-cache invariant).
func NewCorrectStub(name string) *DeterministicStub {
	return &DeterministicStub{name: name, corrupt: false, avail: true}
}

// NewCorruptStub returns a stub whose cache-on output DIVERGES from its
// cache-off output — used as the golden-bad fixture for the equivalence oracle.
func NewCorruptStub(name string) *DeterministicStub {
	return &DeterministicStub{name: name, corrupt: true, avail: true}
}

// NewUnavailableStub returns a stub reporting Available()==false so the harness
// can prove its honest SKIP-with-reason path (§11.4.69 — never a fake PASS).
func NewUnavailableStub(name string) *DeterministicStub {
	return &DeterministicStub{name: name, corrupt: false, avail: false}
}

func (s *DeterministicStub) Name() string    { return s.name }
func (s *DeterministicStub) Available() bool { return s.avail }

// BenchNote implements bench.NoteProvider: it labels the stub's reports as
// modelled, so a stub report is never mistaken for a real measured one (§11.4.6).
func (s *DeterministicStub) BenchNote() string {
	return "Phase 0-1 foundation STUB: durations are from the DeterministicStub latency model (prefillPerTok/decodePerTok constants), NOT measured; real backends replace them (§11.4.197). wall_ms is real measured wall-clock. This report proves the analyzer logic runs, not a real speedup."
}

// genTokens produces genLen output tokens as a deterministic hash-chain seeded
// by the FULL context. This models a temperature=0 / fixed-seed decode: the
// output is a pure function of the input tokens.
func genTokens(ctx []int, corrupt bool) []int {
	h := sha256.New()
	buf := make([]byte, 4)
	for _, t := range ctx {
		binary.LittleEndian.PutUint32(buf, uint32(t))
		_, _ = h.Write(buf)
	}
	cur := h.Sum(nil)
	out := make([]int, genLen)
	for i := 0; i < genLen; i++ {
		next := sha256.Sum256(cur)
		out[i] = int(binary.LittleEndian.Uint32(next[:4]) % vocabSize)
		cur = next[:]
	}
	if corrupt && len(out) > 0 {
		// One-token divergence: the exact silent-corruption class R-CORRECTNESS forbids.
		out[len(out)-1] ^= 1
	}
	return out
}

// Complete implements Backend. It builds the FINAL turn's full context, decodes
// deterministically from it, and accounts prefill honestly: cache-on with a
// present prefix prefills only the New tokens (the reused prefix is skipped);
// cache-off prefills the whole context.
func (s *DeterministicStub) Complete(turns []Turn, cacheOn bool) (CompletionResult, error) {
	if len(turns) == 0 {
		return CompletionResult{}, ErrNoTurns
	}
	last := turns[len(turns)-1]
	full := make([]int, 0, len(last.Prefix)+len(last.New))
	full = append(full, last.Prefix...)
	full = append(full, last.New...)

	// A correct cache changes ONLY timing. corrupt=true injects the divergence.
	gen := genTokens(full, s.corrupt && cacheOn)

	var prefillTokens, prefixReused int
	if cacheOn && len(last.Prefix) > 0 {
		prefillTokens = len(last.New)
		prefixReused = len(last.Prefix)
	} else {
		prefillTokens = len(full)
		prefixReused = 0
	}

	return CompletionResult{
		Tokens:        gen,
		PrefillTokens: prefillTokens,
		PrefixReused:  prefixReused,
		DecodeTokens:  len(gen),
		PrefillDur:    time.Duration(prefillTokens) * prefillPerTok,
		DecodeDur:     time.Duration(len(gen)) * decodePerTok,
		CacheOn:       cacheOn,
		Backend:       s.name,
	}, nil
}
