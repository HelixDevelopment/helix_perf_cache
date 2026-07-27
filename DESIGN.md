# helix_perf_cache — DESIGN

**Revision:** 1
**Last modified:** 2026-07-26T14:40:00Z
**Status:** Phase 0-1 foundation architecture. Interfaces + cache-consistency
model + auto-activation plan + benchmark-harness design. Track A/B backends,
the cache-consistency engine, and the MCP/Skill surfaces are OWED (§11.4.197)
and each operator-review-gated.

---

## 0. Design principles

1. **Project-agnostic (§11.4.28(B)/§11.4.177).** Zero consumer literals. Every
   project value is injected config/DATA; the engine fails closed when config is
   absent — it never guesses a path.
2. **Anti-bluff by construction (§11.4 / §11.4.6 / §11.4.107(10)).** Every number
   is measured and captured; every analyzer self-validates against golden-good +
   golden-bad fixtures.
3. **Honest two-track split (§11.4.112).** Track A (local serving) can earn a real
   prefill-skip speedup; Track B (hosted-API clients) cannot inject a transformer
   KV-cache and never claims "17×".
4. **One interface, many backends.** Local llama.cpp/vLLM/SGLang and hosted
   Anthropic/ccr-provider all implement the same `Backend` contract, so the same
   harness measures them on equal terms.

---

## 1. Core data contract (`pkg/backend`) — REAL now

```go
type Turn struct {
    Prefix []int   // stable prefix (system + tools + prior context) = KV-reuse candidate
    New    []int   // this turn's new user tokens
}

type CompletionResult struct {
    Tokens        []int          // generated token IDs (the correctness evidence)
    PrefillTokens int            // tokens that actually went through prefill
    PrefixReused  int            // prefix tokens served from cache (prefill skipped)
    DecodeTokens  int
    PrefillDur    time.Duration  // TTFT / prefill cost, SEPARATE from decode
    DecodeDur     time.Duration
    CacheOn       bool
    Backend       string
}

type Backend interface {
    Name() string
    Available() bool                                    // honest SKIP source (§11.4.69)
    Complete(turns []Turn, cacheOn bool) (CompletionResult, error)
    // INVARIANT: for a correct backend, Tokens are IDENTICAL for cacheOn and
    // cacheOff on the same turns — the cache changes only TIMING, never output.
}
```

The `DeterministicStub` (this phase) makes output a pure function of the full
context so the analyzers exercise real logic. Later backends replace it:

- **Track A `LlamaCppBackend`** — HTTP to a local `llama-server` (`/v1`
  completions, `cache_prompt: true` / slot reuse), reads `prompt_tokens` and any
  cache-hit signal from the response `usage`. Integrates with HelixLLM's existing
  Go `LlamaCppProvider` (`submodules/helix_llm/internal/brain/llamacpp.go`).
- **Track A `VLLMBackend` / `SGLangBackend`** — OpenAI-compatible `/v1` served
  from a rootless container (PagedAttention APC / RadixAttention), an
  A/B-selectable higher-ceiling alternative to llama.cpp (operator-gated, larger
  blast radius per RISKS.md R-ENGINE-SWAP).
- **Track B `AnthropicPromptCache`** (`pkg/backend/anthropic_promptcache.go`,
  REAL now) — `/v1/messages` with a `cache_control` ephemeral breakpoint on the
  stable prefix; surfaces `usage.cache_read_input_tokens`. `PrefixReused` maps to
  the prompt-cache read (cost + request-latency win), **not** a decode speedup
  (§11.4.112). `go run ./cmd/perfbench -live-b` measures it live when a credential
  is present, else writes an honest `credentials_absent` SKIP report. The live
  NUMBER is MEASUREMENT-PENDING (operator-gated: a real credential + a paid call).
- **Track B `ProviderBackend` (via ccr)** — Anthropic→OpenAI translation through
  claude-code-router. `cache_control` is stripped upstream; the client lever is
  prefix stability + `usage.prompt_cache_hit_tokens` observation + pre-ccr
  compression. Never claims the client "added caching".

Backends declare `Available()==false` when their endpoint/credential is absent →
`bench.Run` returns an honest SKIP report (never a fake PASS).

---

## 2. Equivalence oracle (`pkg/equivalence`) — REAL now

`Compare(cacheOn, cacheOff []int) Verdict` — token-for-token. The two inputs come
from **two distinct execution paths** (cached reuse vs from-scratch recompute),
so agreement is a real property, not a tautology. Divergence = release-blocker
(RISKS.md R-CORRECTNESS). A declared, captured GPU-non-determinism tolerance is a
Phase-3 measurement, never a guess (§11.4.6). Self-validated: correct stub →
Equivalent; corrupt stub → divergence caught with the diverged index.

---

## 3. Speedup analyzer (`pkg/analyzer`) — REAL now, self-validated

`Analyze(cacheOn, cacheOff []CompletionResult) SpeedupVerdict`:

- If cache-on runs claim `CacheOn` but reused **zero** prefix tokens →
  `MislabelDetected=true`, `Speedup=1.0` (no nonexistent speedup ever reported).
- Else speedup = `p50(cacheOff)/p50(cacheOn)` over captured total durations;
  `RealPrefillSkip` iff a prefix was reused **and** cache-on p50 is strictly lower.

Golden fixtures (`testdata/golden_good.json` / `golden_bad.json`) are loaded by
`TestAnalyzerSelfValidation`: good → genuine speedup detected; bad (cache-off
relabelled cache-on) → mislabel caught, speedup 1.0. This is the §11.4.107(10)
proof the analyzer cannot bluff.

---

## 4. Benchmark harness (`pkg/bench` + `cmd/perfbench`) — REAL now

`Run(b, turns, n)` drives N≥10 iterations cache-off + cache-on, records per-iter
prefill/decode/total + real wall-clock, computes p50/p95, folds in the
equivalence verdict + the speedup verdict, and `WriteJSON`s the whole
captured-evidence `Report`. `cmd/perfbench` is the runnable `selfcheck`: it runs
the benchmark, proves the equivalence oracle catches a corrupt cache and the
analyzer catches a mislabel, and **exits non-zero** on any anti-bluff violation.

Three back-ends behind one interface (Track A local, Track B native, Track B
provider) let the same harness compare every lane on equal terms; each honestly
SKIPs when its endpoint/credential is absent.

---

## 5. Cache-consistency engine (OWED, Phase 5) — "never out of date / never out of sync"

Scope: **client-side derived caches only** (compression outputs, a local
prompt-cache index for the local lane, per-alias cache-stats). Server-side
attention KV-cache is owned by the server — this engine only consumes its hit
signals.

- **Content-addressed keys (§11.4.207).** Every entry keyed by
  `sha256(model_id ‖ normalized_prefix_bytes ‖ sampling_params ‖ engine_version)`.
  Identical inputs ⇒ same key ⇒ correct reuse; any input change ⇒ different key ⇒
  automatic miss. A *matched* key is therefore **structurally never stale**.
- **Fingerprint invalidation (§11.4.86).** A sha256-of-sorted-members fingerprint
  over the authoritative inputs (model file hash+mtime, prefix template, provider
  config) re-arms/invalidates on ANY input change.
- **Single-writer per entity (§11.4.206).** Exactly one writer per store entity;
  readers pull, non-owner writes refused; the binary store is NEVER git-merged.
  Namespaced by `(alias, project, model)` so no cross-alias/track/model bleed
  (RISKS.md R-BLEED); the store holds prompt bytes → treated as sensitive
  (§11.4.10: gitignored, `chmod 600`, redaction in stats).
- **Durable + atomic + crash-safe (§11.4.205(6)).** temp→fsync→rename→dir-fsync;
  append-only event ledger for lost-update recovery; advisory lock with
  provably-stale reap (`kill -0`, never steal a live lock, §11.4.180); §9.2 backup
  before any destructive store op; the store is a §11.4.77 derivative
  (rebuildable, never load-bearing-unrecoverable).
- **Self-validated consistency oracle (§11.4.107(10)/§11.4.206(3)).** golden-good
  (fresh entry served), golden-bad (stale-after-input-change → MUST be
  invalidated), negative-control (legitimately-older-but-still-valid → MUST NOT be
  falsely invalidated).
- **Bounded memory / OOM guard (§12.6/§12.12).** Hard byte cap + LRU eviction; any
  local VRAM budget bounded by HelixLLM's `vrambroker` so a cache never OOMs the
  serve host or user session (RISKS.md R-OOM).

---

## 6. Auto-activation surfaces (OWED, Phase 6, §11.4.164 / §11.4.228)

| Surface | Platforms | Fit |
|---|---|---|
| **MCP server** (`perf_benchmark`, `perf_cache_stats`, `perf_prefix_probe`, `perf_compress`) | Claude Code, OpenCode, Gemini CLI, Qwen Code | **PRIMARY** — widest reach, universal. Rootless (§11.4.161). |
| **Skill bundle** (`helix-perf`) | Claude Code + OpenCode | Teaches when to enable prefix-reuse / compression / concise-output; symlinked by the §11.4.164 hook into `.claude/skills/` + `.opencode/skills/`. |
| **Plugin** | Claude Code only | Wraps MCP + skills + a session hook for auto-registration. |
| **ACP** | Zed / Neovim editors | **PROBE-FIRST — expected UNSUPPORTED / category-error** (§11.4.112): ACP is an editor↔agent protocol, not a caching/perf surface. Document the fit-probe verdict, do not force a bad fit. |

Auto-activation is **safe by default**: default to **off/observe** (measure before
changing anything), never silently degrade output, never enable lossy compression
without a per-workload equivalence gate, fail-closed when its config/consistency
oracle is unavailable (§11.4.201). Each surface carries a per-platform compat
declaration `{SUPPORTED|UNTESTED|UNSUPPORTED|CONFIG-ONLY}` (§11.4.228) with a
liveness test + an `EXTENSION_SOURCE.yaml` provenance record. Pull-time
registration is wired via the constitution `post_update_hook.sh` seam (§11.4.164)
so it "auto-activates out of the box the moment any project fetches latest".

---

## 7. OWED work — tracked to completion (§11.4.197)

Phase 3 (Track A backend + measured per-lane numbers) · Phase 4 (Track B levers:
prompt-cache / LLMLingua-class compression off-by-default + equivalence-gated /
concise-output) · Phase 5 (cache-consistency engine §5) · Phase 6 (auto-activation
§6) · Phase 7 (docs/FAQ/diagrams, four-format §11.4.65/§11.4.73) · Phase 8
(constitution-submodule wiring + §11.4.185 manual-QA). None is claimed as shipped
here; the foundation is the scaffold + the honest framing + the self-validated
harness only.
