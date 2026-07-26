package bench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/HelixDevelopment/helix_perf_cache/pkg/backend"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/bench"
)

func turns() []backend.Turn {
	prefix := make([]int, 200)
	for i := range prefix {
		prefix[i] = i + 1
	}
	return []backend.Turn{{Prefix: prefix, New: []int{7001, 7002, 7003, 7004}}}
}

// TestRunCorrectBackend: an available correct backend yields N>=10 iterations,
// a token-equivalent cache, a genuine (non-mislabel) speedup, and JSON that
// round-trips.
func TestRunCorrectBackend(t *testing.T) {
	rep, err := bench.Run(backend.NewCorrectStub("stub-local"), turns(), 3 /* clamped to MinIterations */)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Skipped {
		t.Fatalf("available backend MUST NOT be skipped: %s", rep.SkipReason)
	}
	if rep.N < bench.MinIterations {
		t.Fatalf("N MUST be clamped to >= %d, got %d", bench.MinIterations, rep.N)
	}
	if !rep.Equivalence.Equivalent {
		t.Fatalf("correct backend MUST be token-equivalent: %s", rep.Equivalence.Reason)
	}
	if rep.Speedup.MislabelDetected {
		t.Fatalf("genuine cache MUST NOT be a mislabel: %s", rep.Speedup.Reason)
	}
	if !rep.Speedup.RealPrefillSkip || rep.Speedup.Speedup <= 1.0 {
		t.Fatalf("expected genuine speedup > 1.0, got skip=%v speedup=%.3f", rep.Speedup.RealPrefillSkip, rep.Speedup.Speedup)
	}
	if rep.CacheOffP50Ms <= rep.CacheOnP50Ms {
		t.Fatalf("cache-off p50 (%.1f) MUST exceed cache-on p50 (%.1f)", rep.CacheOffP50Ms, rep.CacheOnP50Ms)
	}

	// JSON round-trip proof.
	out := filepath.Join(t.TempDir(), "report.json")
	if err := bench.WriteJSON(rep, out); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rt bench.Report
	if err := json.Unmarshal(data, &rt); err != nil {
		t.Fatalf("report JSON MUST round-trip: %v", err)
	}
	if rt.Backend != rep.Backend || len(rt.CacheOn) != rep.N {
		t.Fatalf("round-tripped report mismatch: %+v", rt)
	}
}

// TestRunUnavailableBackendSkips: an unavailable backend is honestly SKIPPED,
// never a fake PASS (§11.4.69).
func TestRunUnavailableBackendSkips(t *testing.T) {
	rep, err := bench.Run(backend.NewUnavailableStub("stub-offline"), turns(), 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Skipped || rep.SkipReason == "" {
		t.Fatalf("unavailable backend MUST be skipped with a reason, got %+v", rep)
	}
	if rep.Speedup.RealPrefillSkip {
		t.Fatal("a skipped run MUST NOT claim any speedup")
	}
}

// TestRunCorruptBackendFlagsCorrectness: a buggy cache surfaces as an
// equivalence FAIL in the report — the benchmark never hides a correctness bug
// behind a speedup number.
func TestRunCorruptBackendFlagsCorrectness(t *testing.T) {
	rep, err := bench.Run(backend.NewCorruptStub("stub-corrupt"), turns(), 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Equivalence.Equivalent {
		t.Fatal("corrupt backend MUST report equivalence FAIL — a speedup on wrong output is worse than slow")
	}
}
