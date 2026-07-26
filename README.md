# helix_perf_cache

**Revision:** 1
**Last modified:** 2026-07-26T14:40:00Z
**Status:** Phase 0-1 foundation (repo scaffold + honest two-track framing + anti-bluff benchmark harness skeleton)

A **project-agnostic reusable engine** for LLM inference-performance measurement
and caching, for the Helix family (HelixLLM / HelixAgent / HelixCode) and any
consuming project. Designed to land as a depth-1 submodule under the constitution
submodule (`constitution/submodules/helix_perf_cache/`, the §11.4.28(C) carve-out),
inherited **by reference**, carrying **zero** consumer literals.

---

## The one thing to read first — the honest two-track framing

The popular "KV-cache 17× speedup" idea is **real only for local self-hosted
inference**. Applied naively to a hosted-API client it becomes a bluff. This
engine is built around that split and refuses to blur it.

| Target | Mechanism | Honest expected effect | Is "17×" earned? |
|---|---|---|---|
| **Track A** — local llama.cpp / vLLM / SGLang (HelixLLM's local lane) | Cross-request **prefix KV reuse** (prefill skipped for a matching prefix) | **Prefill-skip** on the reused prefix. 10–20× is real **on high-prefix-overlap agent loops** (stable system+tools+context prefix, short new turn); mixed workloads land lower. | **Conditionally YES** — but only as the *prefill-skip ratio for repeated large prefixes*, **measured per model+lane, never asserted**. |
| **Track B native** — Claude Code / native Claude aliases | Anthropic **prompt caching** (`cache_control`) | ~90% read-cost discount + TTFT-on-hit. **Not** a decode speedup. | **NO.** Attention KV-cache is **structurally impossible** to inject from a hosted-API client (§11.4.112). |
| **Track B providers** — deepseek / xiaomi / kimi / opencode … (via claude-code-router) | Upstream **automatic** prefix caching + client-side **compression** (LLMLingua-class) + **concise output** | Cost win is largely automatic/server-side; the client-controllable win is compression + shorter output — honestly **1.5–3×** on input-heavy turns. | **NO.** Never "17×". |

> **KV-cache is server-side reality for every hosted API.** A client cannot inject
> `past_key_values` into `messages.create` / `chat/completions`. claude-code-router
> even strips `cache_control` before the upstream (the upstream then caches
> automatically anyway). So for hosted aliases the levers are prompt-cache
> placement, context compression, and concise output — **not** a transformer
> KV-cache and **not** a 17× decode speedup.

Full detail + the danger-zone analysis live in the ATMOSphere research package
(`docs/research/kv_cache_perf_20260726/PLAN.md` + `RISKS.md`) that this engine
was scaffolded from, and in [`DESIGN.md`](DESIGN.md).

---

## The anti-bluff benchmark harness (the load-bearing artifact)

The "17×" claim is only non-bluff because a harness **measures** it. This repo
ships a runnable Go harness skeleton whose equivalence logic and self-validation
**genuinely run** (model calls are stubbed at this phase — see "Stub vs real").

- **Real equivalence oracle** (`pkg/equivalence`) — compares a cached multi-turn
  output against a **from-scratch full-context recompute of the SAME turn**,
  token-for-token, temperature=0 / fixed seed. This replaces the pasted guide's
  tautological `text_no_cache == text_with_cache` (two identical computes that
  always pass and prove nothing). Cached≠uncached is a **release-blocking
  correctness defect**.
- **Real benchmark** (`pkg/bench`) — wall-clock + tokens/sec, **prefill/TTFT
  separated from decode**, cache-on vs cache-off, N≥10 iterations, p50/p95,
  captured to JSON. Never emits a theoretical constant as a result.
- **Self-validated analyzer** (`pkg/analyzer`, §11.4.107(10)) — a golden-good
  fixture it must detect a genuine speedup on, and a golden-bad fixture (a
  cache-off run relabelled "cache-on") it must flag as a **mislabel** and report
  **no** speedup on. An analyzer that passes its golden-bad is itself the bluff.

### Run it

```bash
go build ./...        # BUILD_OK
go vet ./...          # clean
go test -race ./...   # all packages ok
go run ./cmd/perfbench  # selfcheck + writes ./qa-results/perfbench.json ; exits non-zero on any anti-bluff violation
```

Captured selfcheck output (this repo, Go 1.26, stub backend):

```
benchmark: backend=stub-local-llamacpp N=12 cacheOff_p50=560ms cacheOn_p50=48ms speedup=11.67x prefix_skip=98% -> qa-results/perfbench.json
equivalence self-validation: golden-good PASS, golden-bad CAUGHT
analyzer self-validation: golden-good speedup DETECTED, golden-bad mislabel CAUGHT
SELFCHECK: PASS
```

The 11.67× is the **stub's** prefill-skip ratio for a 256-token reused prefix +
4 new tokens — it demonstrates the harness math, **not** a real model number.
Real numbers come from the Track-A/Track-B backends wired in later phases.

---

## Stub vs real at this phase (§11.4.6)

| Component | State |
|---|---|
| `Turn` / `CompletionResult` data contract | **REAL** |
| cache-on vs cache-off code paths (prefill accounting) | **REAL** |
| equivalence oracle, speedup analyzer, mislabel detection | **REAL** — genuinely run + self-validate |
| benchmark runner (N≥10, p50/p95, JSON, honest SKIP) | **REAL** |
| the model itself (`DeterministicStub` hash-chain) | **STUB** — pure function of full context, temperature-0-shaped |
| per-token latency (2ms prefill / 5ms decode) | **STUB** constants |
| Track A llama.cpp backend, Track B Anthropic + ccr backends | **OWED** (Phase 3/4, §11.4.197) |
| MCP / Skill auto-activation surfaces | **OWED** (Phase 6, §11.4.164 / §11.4.228) |
| content-addressed cache-consistency engine | **OWED** (Phase 5, §11.4.86 / §11.4.206) |

---

## Layout

```
helix_perf_cache/
├── README.md · DESIGN.md · helix-deps.yaml · .gitignore
├── CLAUDE.md · AGENTS.md · QWEN.md · GEMINI.md   (agent instructions, lockstep §11.4.157)
├── docs/ROOTLESS_CONTAINER.md                    (rootless podman note, §11.4.161)
├── go.mod
├── cmd/perfbench/main.go                          (runnable anti-bluff selfcheck)
├── pkg/backend/     (Backend interface + Turn/CompletionResult + DeterministicStub)
├── pkg/equivalence/ (token-for-token correctness oracle + self-validation)
├── pkg/analyzer/    (speedup analyzer + mislabel detection + golden self-validation)
├── pkg/bench/       (N≥10 p50/p95 benchmark runner + JSON + honest SKIP)
└── testdata/        (golden_good.json / golden_bad.json — analyzer fixtures)
```

## Foundation vs owed (§11.4.197)

**Delivered (Phase 0-1):** dual-remote repo scaffold, honest two-track framing,
project-agnostic contract, the runnable self-validated benchmark harness
skeleton, `helix-deps.yaml`, agent instruction set.

**Owed (later, operator-review-gated):** Track A local prefix-reuse backend
(llama.cpp / vLLM / SGLang) + measured per-lane numbers; Track B prompt-cache /
compression / concise-output levers; the content-addressed cache-consistency
engine; MCP + Skills + plugin auto-activation surfaces; docs/FAQ/diagrams; the
constitution-submodule wiring + mandatory-rule pull-time registration.

## Provenance

Scaffolded 2026-07-26 from the ATMOSphere research package
`docs/research/kv_cache_perf_20260726/` (PLAN + RISKS + honest framing).
Mirrors: [GitHub](https://github.com/HelixDevelopment/helix_perf_cache) ·
[GitLab](https://gitlab.com/helixdevelopment1/helix_perf_cache).
