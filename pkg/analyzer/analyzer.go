// Package analyzer is the SPEEDUP analyzer plus its self-validation contract
// (§11.4.107(10)). It computes whether a genuine prefill-skip speedup occurred
// between cache-off and cache-on runs, and — critically — it DETECTS the
// mislabel where a cache-off run is relabelled "cache-on" without any real
// prefill skip (the exact §11.4.6 bluff the pasted guide's `print("expected
// 10-17x")` produced).
//
// An analyzer that reports a speedup on its golden-bad (mislabelled) fixture is
// itself the bluff — the self-validation test asserts it does not.
package analyzer

import (
	"sort"

	"github.com/HelixDevelopment/helix_perf_cache/pkg/backend"
)

// SpeedupVerdict is the analyzer's structured output.
type SpeedupVerdict struct {
	Backend          string  `json:"backend"`
	ClaimedCacheOn   bool    `json:"claimed_cache_on"`
	RealPrefillSkip  bool    `json:"real_prefill_skip"` // a genuine prefill skip was measured
	MislabelDetected bool    `json:"mislabel_detected"` // "cache-on" claimed but no prefix reused
	PrefixSkipRatio  float64 `json:"prefix_skip_ratio"` // reused / (reused + prefilled) tokens, 0..1
	P50CacheOnMs     float64 `json:"p50_cache_on_ms"`
	P50CacheOffMs    float64 `json:"p50_cache_off_ms"`
	Speedup          float64 `json:"speedup"` // p50_off / p50_on  (1.0 == none) — END-TO-END (prefill+decode)
	// Prefill-only view: the KV-prefix cache speeds up PREFILL only; decode is
	// unchanged. PrefillSpeedup is the honest "what the KV-cache buys" number
	// (what the popular "10-17× KV-cache" claim refers to); Speedup above is the
	// END-TO-END number an end user actually experiences (diluted by decode).
	// Reporting BOTH is the §11.4.6 disambiguation this engine exists for.
	PrefillP50CacheOnMs  float64 `json:"prefill_p50_cache_on_ms"`
	PrefillP50CacheOffMs float64 `json:"prefill_p50_cache_off_ms"`
	PrefillSpeedup       float64 `json:"prefill_speedup"` // prefill_p50_off / prefill_p50_on
	Reason               string  `json:"reason"`
}

// Analyze compares cache-on runs against cache-off runs and returns a verdict.
//
// Contract (the anti-bluff core):
//   - If the cache-on runs claim CacheOn but reused ZERO prefix tokens, the
//     "cache" is a mislabel: MislabelDetected=true, RealPrefillSkip=false,
//     Speedup forced to 1.0. NO nonexistent speedup is ever reported.
//   - Otherwise the speedup is p50(cache-off) / p50(cache-on) over the captured
//     total durations, and RealPrefillSkip is true iff a prefix was reused AND
//     the cache-on p50 is strictly lower.
func Analyze(cacheOn, cacheOff []backend.CompletionResult) SpeedupVerdict {
	v := SpeedupVerdict{Speedup: 1.0}
	if len(cacheOn) == 0 || len(cacheOff) == 0 {
		v.Reason = "insufficient runs"
		return v
	}
	v.Backend = cacheOn[0].Backend
	v.ClaimedCacheOn = allClaimCacheOn(cacheOn)

	reused, prefilled := aggregatePrefix(cacheOn)
	if v.ClaimedCacheOn && reused == 0 {
		// A cache-off run relabelled as cache-on: no real prefill was skipped.
		v.MislabelDetected = true
		v.RealPrefillSkip = false
		v.Speedup = 1.0
		v.Reason = "MISLABEL: cache-on claimed but zero prefix tokens reused — no real prefill skip"
		v.P50CacheOnMs = p50Ms(cacheOn)
		v.P50CacheOffMs = p50Ms(cacheOff)
		return v
	}

	v.P50CacheOnMs = p50Ms(cacheOn)
	v.P50CacheOffMs = p50Ms(cacheOff)
	v.PrefillP50CacheOnMs = prefillP50Ms(cacheOn)
	v.PrefillP50CacheOffMs = prefillP50Ms(cacheOff)
	if reused+prefilled > 0 {
		v.PrefixSkipRatio = float64(reused) / float64(reused+prefilled)
	}
	if v.P50CacheOnMs > 0 {
		v.Speedup = v.P50CacheOffMs / v.P50CacheOnMs
	}
	if v.PrefillP50CacheOnMs > 0 {
		v.PrefillSpeedup = v.PrefillP50CacheOffMs / v.PrefillP50CacheOnMs
	}
	v.RealPrefillSkip = reused > 0 && v.P50CacheOnMs < v.P50CacheOffMs
	if v.RealPrefillSkip {
		v.Reason = "genuine prefill-skip speedup measured (cache-on p50 < cache-off p50, prefix reused)"
	} else {
		v.Reason = "no genuine speedup detected"
	}
	return v
}

func allClaimCacheOn(rs []backend.CompletionResult) bool {
	for _, r := range rs {
		if !r.CacheOn {
			return false
		}
	}
	return true
}

// aggregatePrefix returns the median reused/prefilled prefix counts so a single
// noisy run cannot flip the mislabel verdict.
func aggregatePrefix(rs []backend.CompletionResult) (reused, prefilled int) {
	reusedVals := make([]int, len(rs))
	prefVals := make([]int, len(rs))
	for i, r := range rs {
		reusedVals[i] = r.PrefixReused
		prefVals[i] = r.PrefillTokens
	}
	sort.Ints(reusedVals)
	sort.Ints(prefVals)
	return reusedVals[len(reusedVals)/2], prefVals[len(prefVals)/2]
}

func p50Ms(rs []backend.CompletionResult) float64 {
	totals := make([]float64, len(rs))
	for i, r := range rs {
		totals[i] = float64((r.PrefillDur + r.DecodeDur).Milliseconds())
	}
	sort.Float64s(totals)
	return totals[len(totals)/2]
}

// prefillP50Ms returns the median prefill-only duration (the KV-prefix cache's
// direct effect surface — decode is unchanged).
func prefillP50Ms(rs []backend.CompletionResult) float64 {
	vals := make([]float64, len(rs))
	for i, r := range rs {
		vals[i] = float64(r.PrefillDur.Milliseconds())
	}
	sort.Float64s(vals)
	return vals[len(vals)/2]
}
