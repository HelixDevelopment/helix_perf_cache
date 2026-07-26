# AGENTS.md — helix_perf_cache (for all AI coding agents)

## INHERITED FROM constitution/CLAUDE.md

All rules in `constitution/CLAUDE.md` (and the `constitution/Constitution.md` it
references) apply unconditionally when this engine is consumed inside a project
that carries the Helix Constitution submodule. This file's engine-specific rules
below EXTEND those universal rules — they never weaken any clause. When consumed
standalone (developing the engine itself), the same anti-bluff discipline
(§11.4 / §11.4.6 / §11.4.107(10)) applies.

## What this engine is

`helix_perf_cache` is a **project-agnostic reusable engine** (§11.4.28 / §11.4.177)
for LLM inference-performance measurement and cache work across the Helix family
and any consuming project. It is designed to land as a depth-1 submodule under
the constitution submodule (`constitution/submodules/helix_perf_cache/`, the
§11.4.28(C) carve-out). Status: **Phase 0-1 foundation** — the honest two-track
framing, the anti-bluff benchmark harness skeleton, and the self-validated
analyzers. Track A/B implementation + MCP/Skill surfaces + cache-consistency
engine continue in later phases (§11.4.197), each operator-review-gated.

## The HONEST two-track framing (§11.4.6 / §11.4.112 — the load-bearing rule)

The "17× KV-cache" idea is REAL only for **local self-hosted inference**. This
engine MUST NOT let any deliverable claim "17× for Claude Code" or "17× for a
hosted-API alias" — that is a §11.4.112 structural-impossibility bluff.

- **Track A — local llama.cpp / vLLM / SGLang serving** (HelixLLM's local lane):
  cross-request **prefix KV reuse** yields a real **prefill-skip** speedup.
  10–20× is achievable ON HIGH-PREFIX-OVERLAP agent loops; typical mixed
  workloads land lower. The number is **measured per model+lane, NEVER asserted**.
- **Track B — hosted-API clients** (Claude Code, native Claude aliases, and
  provider aliases via claude-code-router): attention KV-cache is
  **STRUCTURALLY IMPOSSIBLE** to inject from the client (§11.4.112) — the weights
  run on the provider's servers. The real, modest client-side levers are
  **prompt caching** (Anthropic `cache_control`), **context compression**
  (LLMLingua-class), and **concise output**: honestly 1.5–3× on input-heavy
  turns, and a **cost** win, NEVER a decode speedup and NEVER "17×".

Naming discipline: `prefix-cache` / `KV-prefix-reuse` for the real transformer
work. Do NOT conflate it with a session-history "KV cache" (HelixLLM already has
one named `brain.KVCache`, which is unrelated). Every "KV-cache" mention MUST be
disambiguated.

## Anti-bluff rules for this engine (non-negotiable)

1. **No faked benchmarks.** Every speedup number is wall-clock + tokens/sec
   captured to JSON, prefill separated from decode, N≥10 (§11.4.50), p50/p95,
   per model+backend. NEVER emit a theoretical constant as a result (the pasted
   guide's `print("expected 10-17x")` is the exact bluff this engine replaces).
2. **No tautological equivalence tests.** The equivalence oracle compares a
   cached multi-turn output against a from-scratch full-context recompute of the
   SAME turn, token-for-token, temperature=0 / fixed seed — NOT two identical
   computes. Cached≠uncached is a release-blocking correctness defect.
3. **Every analyzer is self-validated (§11.4.107(10)).** It ships a golden-good
   fixture it must PASS and a golden-bad fixture it must FAIL/flag. An analyzer
   that passes its golden-bad is itself the bluff. Run `go run ./cmd/perfbench`
   (the `selfcheck`) — it exits non-zero if any anti-bluff invariant breaks.
4. **Honest SKIP, never a fake PASS (§11.4.69).** An unavailable backend
   (endpoint/credential absent) is SKIPPED-with-reason, never reported PASS.
5. **Stub vs real is always documented (§11.4.6).** At this phase model calls are
   stubbed by a DeterministicStub; the equivalence + speedup + self-validation
   logic genuinely runs. Every stub is labelled in-source and in the report.

## Project-agnostic contract (§11.4.28(B) / §11.4.177)

ZERO project literals — no `com.atmosphere.*`, no device serials, no `/mnt/trackN`
paths, no region endpoints, no consumer-specific model ids. Every project value
(endpoints, alias list, provider matrix, cache-store path, calibrated thresholds)
is CONSUMER-SUPPLIED config/DATA, injected at runtime — never engine code. The
engine fails closed with an actionable message when its config is absent; it
never guesses a path.

## Build / test / hygiene

- `go build ./...` · `go vet ./...` · `gofmt -l .` (must be empty) · `go test -race ./...` — all clean before any commit.
- `go run ./cmd/perfbench` runs the anti-bluff selfcheck and writes a captured
  benchmark JSON under `qa-results/` (gitignored, regenerable §11.4.77).
- Rootless-only for any container (§11.4.161) — rootless podman via the Containers
  submodule, never rootful docker/sudo. See `docs/ROOTLESS_CONTAINER.md`.
- No force-push, ever (§11.4.113): merge onto latest `main`, ff-only, push to all
  configured upstreams (github + gitlab).

## Where the detail lives

- `README.md` — what it is + the honest speedup table + quick start.
- `DESIGN.md` — Track A/B interfaces, the content-addressed cache-consistency
  engine (§11.4.86 fingerprint + §11.4.206 single-writer), the MCP/Skill
  auto-activation plan (§11.4.164 / §11.4.228), the benchmark-harness design.
