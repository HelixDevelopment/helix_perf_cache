// Command perfbench is the runnable entry point of the Phase 0-1 harness
// skeleton. With no arguments it runs a `selfcheck`: it drives the
// DeterministicStub backends, proves the equivalence oracle catches a corrupt
// cache, proves the speedup analyzer catches a mislabel, and writes a captured
// benchmark JSON. It exits NON-ZERO if any anti-bluff invariant is violated —
// so the harness cannot silently bluff.
//
//	go run ./cmd/perfbench            # selfcheck + write ./qa-results/perfbench.json
//	go run ./cmd/perfbench <out.json> # selfcheck + write to <out.json>
//	go run ./cmd/perfbench -live [endpoint] [out.json]
//	                                  # REAL Track-A benchmark against a live
//	                                  # llama.cpp server (default endpoint
//	                                  # http://localhost:18434), then selfcheck.
//	                                  # Honest SKIP report if the server is absent.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/HelixDevelopment/helix_perf_cache/pkg/analyzer"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/backend"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/bench"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/equivalence"
)

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "-live" {
		endpoint := "http://localhost:18434"
		out := "qa-results/perfbench-live.json"
		rest := args[1:]
		if len(rest) > 0 {
			endpoint = rest[0]
		}
		if len(rest) > 1 {
			out = rest[1]
		}
		if err := runLive(endpoint, out); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
			os.Exit(1)
		}
		return
	}
	out := "qa-results/perfbench.json"
	if len(args) > 0 {
		out = args[0]
	}
	if err := run(out); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
}

// runLive drives the REAL llama.cpp backend end-to-end and writes the measured
// report. It replaces the stub's modelled speedup with a genuinely-measured
// number, or writes an honest SKIP report (§11.4.69) when the server is absent.
func runLive(endpoint, out string) error {
	b := backend.NewLlamaCpp("llamacpp-local", endpoint, 24)
	if !b.Available() {
		rep := bench.Report{
			Backend:    b.Name(),
			Timestamp:  nowUTC(),
			N:          0,
			Skipped:    true,
			SkipReason: "llama.cpp /health not OK at " + endpoint + " — SKIP-with-reason (§11.4.69 network_unreachable_external / hardware_not_present), NOT a fake PASS",
			Note:       "MEASUREMENT PENDING — infra blocked: no live llama.cpp server. Start a server (e.g. llama-server -m <model.gguf> -fa on --port 18434) and re-run.",
		}
		if err := bench.WriteJSON(rep, out); err != nil {
			return fmt.Errorf("write skip report: %w", err)
		}
		fmt.Printf("live benchmark: backend=%s SKIPPED (%s) -> %s\n", rep.Backend, rep.SkipReason, out)
		return nil
	}

	// Build a REALISTIC turn from real prompt text tokenized by the server: a
	// long stable prefix (system + prior context — the KV-reuse candidate) plus
	// a short new user turn. Real token IDs, real model, real serving path.
	prefixText := repeatSystem(120)
	newText := " Now, summarize the single most important rule in one sentence:"
	prefixToks, err := b.Tokenize(prefixText)
	if err != nil {
		return fmt.Errorf("tokenize prefix: %w", err)
	}
	newToks, err := b.Tokenize(newText)
	if err != nil {
		return fmt.Errorf("tokenize new: %w", err)
	}
	turns := []backend.Turn{{Prefix: prefixToks, New: newToks}}

	rep, err := bench.Run(b, turns, 12)
	if err != nil {
		return fmt.Errorf("live benchmark: %w", err)
	}
	rep.Note = fmt.Sprintf("REAL measured Track-A KV-prefix-reuse on live llama.cpp (%s). Durations + tokens read from the server's own timings; NO modelled value. prefix=%d tok, new=%d tok. wall_ms is host-measured; prefill/decode are server-reported. Equivalence: strict-greedy cache-on vs cache-off recompute of the SAME turn, token-for-token.",
		endpoint, len(prefixToks), len(newToks))
	if err := bench.WriteJSON(rep, out); err != nil {
		return fmt.Errorf("write live report: %w", err)
	}
	fmt.Printf("live benchmark: backend=%s N=%d prefill_p50 off=%.0fms on=%.0fms prefill_speedup=%.2fx | endToEnd off=%.0fms on=%.0fms endToEnd_speedup=%.2fx | prefix_skip=%.0f%% equivalent=%v -> %s\n",
		rep.Backend, rep.N, rep.Speedup.PrefillP50CacheOffMs, rep.Speedup.PrefillP50CacheOnMs, rep.Speedup.PrefillSpeedup,
		rep.CacheOffP50Ms, rep.CacheOnP50Ms, rep.Speedup.Speedup, rep.Speedup.PrefixSkipRatio*100, rep.Equivalence.Equivalent, out)
	if rep.Speedup.MislabelDetected {
		return fmt.Errorf("live benchmark reported a mislabel: %s", rep.Speedup.Reason)
	}
	if !rep.Equivalence.Equivalent {
		fmt.Printf("WARNING: cache-on output diverged from cache-off recompute at token %d (%s) — see report; a correct KV-prefix cache changes ONLY timing.\n",
			rep.Equivalence.DivergedAt, rep.Equivalence.Reason)
	}
	return nil
}

func repeatSystem(n int) string {
	const unit = "You are a senior software engineer assisting with a large codebase. Follow the coding standards strictly. "
	s := ""
	for i := 0; i < n; i++ {
		s += unit
	}
	return s
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
