package analyzer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/HelixDevelopment/helix_perf_cache/pkg/analyzer"
	"github.com/HelixDevelopment/helix_perf_cache/pkg/backend"
)

type fixture struct {
	CacheOff []backend.CompletionResult `json:"cache_off"`
	CacheOn  []backend.CompletionResult `json:"cache_on"`
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", name)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %s: %v", p, err)
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", p, err)
	}
	return f
}

// TestAnalyzerSelfValidation is the §11.4.107(10) self-validation: the SAME
// analyzer, run on the golden-good fixture, MUST detect a genuine speedup; run
// on the golden-bad (mislabelled) fixture, it MUST detect the mislabel and
// report NO speedup. An analyzer that reports a speedup on golden-bad is itself
// the bluff, and this test fails it.
func TestAnalyzerSelfValidation(t *testing.T) {
	good := loadFixture(t, "golden_good.json")
	gv := analyzer.Analyze(good.CacheOn, good.CacheOff)
	if gv.MislabelDetected {
		t.Fatalf("golden-good MUST NOT be flagged as a mislabel: %s", gv.Reason)
	}
	if !gv.RealPrefillSkip {
		t.Fatalf("golden-good MUST show a genuine prefill skip: %s", gv.Reason)
	}
	if gv.Speedup <= 1.0 {
		t.Fatalf("golden-good MUST measure speedup > 1.0, got %.3f", gv.Speedup)
	}
	if gv.PrefixSkipRatio <= 0 {
		t.Fatalf("golden-good MUST report a positive prefix-skip ratio, got %.3f", gv.PrefixSkipRatio)
	}

	bad := loadFixture(t, "golden_bad.json")
	bv := analyzer.Analyze(bad.CacheOn, bad.CacheOff)
	if !bv.MislabelDetected {
		t.Fatal("golden-bad (cache-off relabelled cache-on) MUST be flagged as a mislabel — the analyzer is a bluff")
	}
	if bv.RealPrefillSkip {
		t.Fatal("golden-bad MUST NOT claim a real prefill skip")
	}
	if bv.Speedup != 1.0 {
		t.Fatalf("golden-bad MUST report speedup 1.0 (none), got %.3f — a nonexistent speedup was reported", bv.Speedup)
	}
}

// TestInsufficientRuns: empty inputs never fabricate a speedup.
func TestInsufficientRuns(t *testing.T) {
	v := analyzer.Analyze(nil, nil)
	if v.Speedup != 1.0 || v.RealPrefillSkip {
		t.Fatalf("empty input MUST default to no speedup, got %+v", v)
	}
}
