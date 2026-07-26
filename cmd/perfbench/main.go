// Command perfbench is the runnable entry point of the Phase 0-1 harness
// skeleton. With no arguments it runs a `selfcheck`: it drives the
// DeterministicStub backends, proves the equivalence oracle catches a corrupt
// cache, proves the speedup analyzer catches a mislabel, and writes a captured
// benchmark JSON. It exits NON-ZERO if any anti-bluff invariant is violated —
// so the harness cannot silently bluff.
//
//	go run ./cmd/perfbench            # selfcheck + write ./qa-results/perfbench.json
//	go run ./cmd/perfbench <out.json> # selfcheck + write to <out.json>
package main

import (
	"fmt"
	"os"

	"github.com/HelixDevelopment/helix_perf_cache/pkg/analyzer"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/backend"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/bench"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/equivalence"
)

func main() {
	out := "qa-results/perfbench.json"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := run(out); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
}

func run(out string) error {
	turns := buildTurns()

	// 1) Benchmark the correct local stub (Track-A-shaped path).
	rep, err := bench.Run(backend.NewCorrectStub("stub-local-llamacpp"), turns, 12)
	if err != nil {
		return fmt.Errorf("benchmark: %w", err)
	}
	if err := bench.WriteJSON(rep, out); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	fmt.Printf("benchmark: backend=%s N=%d cacheOff_p50=%.0fms cacheOn_p50=%.0fms speedup=%.2fx prefix_skip=%.0f%% -> %s\n",
		rep.Backend, rep.N, rep.CacheOffP50Ms, rep.CacheOnP50Ms, rep.Speedup.Speedup, rep.Speedup.PrefixSkipRatio*100, out)

	// 2) Equivalence oracle self-validation: correct stub PASS, corrupt stub caught.
	okOn, _ := backend.NewCorrectStub("c").Complete(turns, true)
	okOff, _ := backend.NewCorrectStub("c").Complete(turns, false)
	if v := equivalence.Compare(okOn.Tokens, okOff.Tokens); !v.Equivalent {
		return fmt.Errorf("equivalence golden-good FAILED: %s", v.Reason)
	}
	badOn, _ := backend.NewCorruptStub("b").Complete(turns, true)
	badOff, _ := backend.NewCorruptStub("b").Complete(turns, false)
	if v := equivalence.Compare(badOn.Tokens, badOff.Tokens); v.Equivalent {
		return fmt.Errorf("equivalence golden-bad NOT caught — the oracle is blind (a bluff)")
	}
	fmt.Println("equivalence self-validation: golden-good PASS, golden-bad CAUGHT")

	// 3) Speedup analyzer self-validation: genuine skip vs relabelled mislabel.
	genOn, genOff := makeRuns(true /*reuse*/)
	gv := analyzer.Analyze(genOn, genOff)
	if gv.MislabelDetected || !gv.RealPrefillSkip || gv.Speedup <= 1.0 {
		return fmt.Errorf("analyzer golden-good FAILED: %+v", gv)
	}
	mislabelOn, mislabelOff := makeRuns(false /*no reuse, relabelled cache-on*/)
	bv := analyzer.Analyze(mislabelOn, mislabelOff)
	if !bv.MislabelDetected || bv.Speedup != 1.0 {
		return fmt.Errorf("analyzer golden-bad NOT caught — reported a nonexistent speedup: %+v", bv)
	}
	fmt.Println("analyzer self-validation: golden-good speedup DETECTED, golden-bad mislabel CAUGHT")
	fmt.Println("SELFCHECK: PASS")
	return nil
}

func buildTurns() []backend.Turn {
	prefix := make([]int, 256)
	for i := range prefix {
		prefix[i] = i + 1
	}
	return []backend.Turn{{Prefix: prefix, New: []int{5001, 5002, 5003, 5004}}}
}

// makeRuns fabricates cache-on/off result sets. reuse=true => genuine prefill
// skip; reuse=false => cache-off runs relabelled cache-on (the mislabel).
func makeRuns(reuse bool) (on, off []backend.CompletionResult) {
	for i := 0; i < 10; i++ {
		off = append(off, backend.CompletionResult{
			PrefillTokens: 260, PrefixReused: 0, DecodeTokens: 8,
			PrefillDur: 260 * 2e6, DecodeDur: 8 * 5e6, CacheOn: false, Backend: "gen",
		})
		if reuse {
			on = append(on, backend.CompletionResult{
				PrefillTokens: 4, PrefixReused: 256, DecodeTokens: 8,
				PrefillDur: 4 * 2e6, DecodeDur: 8 * 5e6, CacheOn: true, Backend: "gen",
			})
		} else {
			on = append(on, backend.CompletionResult{ // relabelled: cache_on=true but no reuse
				PrefillTokens: 260, PrefixReused: 0, DecodeTokens: 8,
				PrefillDur: 260 * 2e6, DecodeDur: 8 * 5e6, CacheOn: true, Backend: "gen",
			})
		}
	}
	return on, off
}
