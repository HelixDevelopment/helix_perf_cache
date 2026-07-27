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
ships a runnable Go harness whose equivalence logic and self-validation
**genuinely run**, and a **REAL Track-A backend** (`-live`) that drives a live
`llama.cpp` server and reports MEASURED numbers (see "REAL measured Track-A
result" below). The `DeterministicStub` remains only as the self-validation
fixture for the analyzers — never as a presented result.

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
go test -race ./...   # all packages ok (live backend test SKIPs if no server)
go run ./cmd/perfbench          # selfcheck (stub analyzers) + writes ./qa-results/perfbench.json
go run ./cmd/perfbench -live [endpoint] [out.json]   # REAL Track-A benchmark vs a live llama.cpp server
                                                     # default endpoint http://localhost:18434
```

The `selfcheck` (no args) validates the harness's OWN logic against the
`DeterministicStub`; its `speedup=11.67x` line is the **stub's** modelled
prefill-skip ratio (256-token reused prefix + 4 new tokens), demonstrating the
harness math — **NOT a measured model number**. The real number comes from
`-live`.

### REAL measured Track-A result (live llama.cpp — MEASURED, not asserted)

Captured against a live `llama.cpp` server (Qwen3-Coder-30B-A3B Q4_K_M, flash
attention on, q8_0 KV-cache; endpoint `:18434`), N=12, 2293-token prompt
(2281-token stable prefix + a short new turn), strict greedy (temp 0, top_k 1),
every duration + token read from the server's own `timings`:

```
live benchmark: backend=llamacpp-local N=12 \
  prefill_p50 off=201ms on=13ms prefill_speedup=15.46x | \
  endToEnd off=288ms on=97ms endToEnd_speedup=2.96x | \
  prefix_skip=99% equivalent=true -> qa-results/perfbench-live.json
```

Read this honestly (§11.4.6), and note the **two distinct numbers** are the
whole point:

- **`prefill_speedup` ≈ 15×** — what the KV-prefix cache actually buys: the
  reused prefix's KV is skipped, so prompt-processing drops from ~200 ms to
  ~13 ms (2281 of 2293 tokens served from cache). This is the number the popular
  "10–17× KV-cache" claim refers to, and it lands in that band **for this
  high-prefix-overlap shape** — measured, per model + lane, never asserted.
- **`endToEnd_speedup` ≈ 3×** — what an end user actually experiences on a
  24-token completion: decode time is UNCHANGED by the cache, so it dilutes the
  prefill win. Longer prefixes / shorter outputs push end-to-end toward the
  prefill number; longer outputs push it lower.
- **`equivalent=true`** — the equivalence oracle compared the cache-on output
  against a from-scratch full-context recompute of the SAME turn (two genuinely
  distinct server paths: `cache_n=2281` reuse vs `cache_n=0` full prefill) and
  found them **token-for-token identical**. This is NOT the tautology the pasted
  guide used. *(Measured caveat: under the server's DEFAULT sampler at temp 0,
  quantized-KV numerics can flip near-tie tokens; the benchmark uses strict
  greedy — top_k 1 — which is the correct deterministic comparison and yields
  exact identity.)*

If no server is reachable, `-live` writes an honest SKIP report
(`skipped:true`, `MEASUREMENT PENDING — infra blocked`), never a fake PASS
(§11.4.69).

---

## Stub vs real at this phase (§11.4.6)

| Component | State |
|---|---|
| `Turn` / `CompletionResult` data contract | **REAL** |
| cache-on vs cache-off code paths (prefill accounting) | **REAL** |
| equivalence oracle, speedup analyzer, mislabel detection | **REAL** — genuinely run + self-validate |
| benchmark runner (N≥10, p50/p95, JSON, honest SKIP) | **REAL** |
| **Track A llama.cpp backend (`pkg/backend/llamacpp.go`)** | **REAL** — drives a live `/completion` server, every timing/token read from the server, real prefix-reuse measured (see live result above) |
| the `DeterministicStub` model (selfcheck only) | **STUB** — pure function of full context, temperature-0-shaped; used ONLY to self-validate the analyzers, never presented as a measured result |
| stub per-token latency (2ms prefill / 5ms decode) | **STUB** constants — selfcheck only |
| **Track B Anthropic prompt-cache backend (`pkg/backend/anthropic_promptcache.go`)** | **REAL backend + honest-SKIP harness** — drives `/v1/messages` with `cache_control`, reads `usage.cache_read_input_tokens` (prompt-cache read = cost + latency win, **NOT** a decode speedup, §11.4.112). `go run ./cmd/perfbench -live-b` measures it live when `ANTHROPIC_API_KEY` is set; otherwise writes an honest `skipped:true` `credentials_absent` / **MEASUREMENT PENDING** report — never a fake PASS, never an autonomous paid call. **No live number is claimed** (this host has no Anthropic credential). |
| Track B ccr-provider compression / concise-output levers | **OWED** (Phase 4, §11.4.197) |
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
