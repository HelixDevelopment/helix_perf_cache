// Package bench is the REAL benchmark runner: it drives a backend N>=10 times
// cache-off and cache-on, separates prefill/TTFT from decode, computes p50/p95,
// and serialises the whole thing to JSON captured evidence (§11.4.5 / §11.4.69 /
// §11.4.50). It NEVER emits a theoretical constant as a result — the pasted
// guide's `print("expected 10-17x")` is exactly what this replaces.
package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/HelixDevelopment/helix_perf_cache/pkg/analyzer"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/backend"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/equivalence"
)

// MinIterations is the anti-bluff floor for a valid benchmark (§11.4.50 — one
// run is not a distribution).
const MinIterations = 10

// IterationTiming is one captured iteration.
type IterationTiming struct {
	Iter        int     `json:"iter"`
	TotalMs     float64 `json:"total_ms"`
	PrefillMs   float64 `json:"prefill_ms"`
	DecodeMs    float64 `json:"decode_ms"`
	WallMs      float64 `json:"wall_ms"` // real measured wall-clock around the call
	DecodeToks  int     `json:"decode_toks"`
	TokPerSec   float64 `json:"tok_per_sec"`
	PrefixReuse int     `json:"prefix_reuse"`
}

// Report is the full captured-evidence artefact written to JSON.
type Report struct {
	Backend       string                  `json:"backend"`
	Timestamp     string                  `json:"timestamp"`
	N             int                     `json:"n"`
	Skipped       bool                    `json:"skipped"`
	SkipReason    string                  `json:"skip_reason,omitempty"`
	Note          string                  `json:"note"`
	CacheOffP50Ms float64                 `json:"cache_off_p50_ms"`
	CacheOffP95Ms float64                 `json:"cache_off_p95_ms"`
	CacheOnP50Ms  float64                 `json:"cache_on_p50_ms"`
	CacheOnP95Ms  float64                 `json:"cache_on_p95_ms"`
	Equivalence   equivalence.Verdict     `json:"equivalence"`
	Speedup       analyzer.SpeedupVerdict `json:"speedup"`
	CacheOff      []IterationTiming       `json:"cache_off"`
	CacheOn       []IterationTiming       `json:"cache_on"`
}

// Run executes the benchmark for a backend against a fixed turn set.
// If the backend is unavailable it returns an honest SKIP report (§11.4.69 —
// never a fake PASS). n is clamped to MinIterations.
func Run(b backend.Backend, turns []backend.Turn, n int) (Report, error) {
	if n < MinIterations {
		n = MinIterations
	}
	rep := Report{
		Backend:   b.Name(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		N:         n,
		Note:      "Phase 0-1 foundation: durations are from the DeterministicStub latency model; real backends replace them (§11.4.197). wall_ms is real measured wall-clock.",
	}
	if !b.Available() {
		rep.Skipped = true
		rep.SkipReason = "backend unavailable (endpoint/credential absent) — SKIP-with-reason, not a fake PASS"
		return rep, nil
	}

	offResults := make([]backend.CompletionResult, 0, n)
	onResults := make([]backend.CompletionResult, 0, n)
	for i := 0; i < n; i++ {
		off, wallOff, err := timed(b, turns, false)
		if err != nil {
			return rep, fmt.Errorf("cache-off iter %d: %w", i, err)
		}
		on, wallOn, err := timed(b, turns, true)
		if err != nil {
			return rep, fmt.Errorf("cache-on iter %d: %w", i, err)
		}
		offResults = append(offResults, off)
		onResults = append(onResults, on)
		rep.CacheOff = append(rep.CacheOff, timing(i, off, wallOff))
		rep.CacheOn = append(rep.CacheOn, timing(i, on, wallOn))
	}

	rep.CacheOffP50Ms, rep.CacheOffP95Ms = pcts(rep.CacheOff)
	rep.CacheOnP50Ms, rep.CacheOnP95Ms = pcts(rep.CacheOn)

	// Correctness oracle: cache-on final-turn output vs cache-off recompute.
	rep.Equivalence = equivalence.Compare(onResults[0].Tokens, offResults[0].Tokens)
	// Speedup analyzer with mislabel detection.
	rep.Speedup = analyzer.Analyze(onResults, offResults)
	return rep, nil
}

func timed(b backend.Backend, turns []backend.Turn, cacheOn bool) (backend.CompletionResult, float64, error) {
	start := time.Now()
	r, err := b.Complete(turns, cacheOn)
	wall := float64(time.Since(start).Microseconds()) / 1000.0
	return r, wall, err
}

func timing(i int, r backend.CompletionResult, wallMs float64) IterationTiming {
	prefillMs := float64(r.PrefillDur.Milliseconds())
	decodeMs := float64(r.DecodeDur.Milliseconds())
	tps := 0.0
	if decodeMs > 0 {
		tps = float64(r.DecodeTokens) / (decodeMs / 1000.0)
	}
	return IterationTiming{
		Iter:        i,
		TotalMs:     prefillMs + decodeMs,
		PrefillMs:   prefillMs,
		DecodeMs:    decodeMs,
		WallMs:      wallMs,
		DecodeToks:  r.DecodeTokens,
		TokPerSec:   tps,
		PrefixReuse: r.PrefixReused,
	}
}

func pcts(ts []IterationTiming) (p50, p95 float64) {
	if len(ts) == 0 {
		return 0, 0
	}
	vals := make([]float64, len(ts))
	for i, t := range ts {
		vals[i] = t.TotalMs
	}
	sort.Float64s(vals)
	return pct(vals, 0.50), pct(vals, 0.95)
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

// WriteJSON writes the report to path (creating parent dirs).
func WriteJSON(rep Report, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
