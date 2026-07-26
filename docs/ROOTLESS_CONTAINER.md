# Rootless container policy (§11.4.161)

**Revision:** 1
**Last modified:** 2026-07-26T14:40:00Z

`helix_perf_cache` runs **every** containerized workload via **rootless podman**,
never rootful docker or `sudo`. This is a Helix Constitution invariant
(§11.4.161) and applies to every future container this engine ships.

## Current state (Phase 0-1 foundation)

The engine ships **no container** yet. It is pure Go stdlib — `go build` /
`go test` / `go run ./cmd/perfbench` need no container runtime at all. There is
therefore nothing to run rootless at this phase; this note records the binding
policy for the containers that arrive with later phases.

## Where containers arrive (OWED, §11.4.197)

- **Track A (Phase 3)** — an optional rootless bench-lane container serving
  vLLM (PagedAttention) / SGLang (RadixAttention) on `/v1`, A/B'd against the
  zero-container llama.cpp `cache_prompt` baseline.
- **Track B (Phase 4)** — an optional rootless LLMLingua-class prompt-compressor
  service (off by default, equivalence-gated per workload).

## Rules for those containers

1. **Rootless podman only** — never rootful docker, never `sudo`, never any
   escalation to root.
2. **Consumed via the Containers submodule** (`git@github.com:vasic-digital/containers.git`,
   §11.4.76) — no hand-rolled `docker`/`podman` invocations outside its
   `pkg/boot` / `pkg/compose` / `pkg/health` layers. The dependency is declared
   in `helix-deps.yaml` **in the phase that first ships a container** (not before
   — §11.4.6, declared only when real).
3. **Explicit `mem_limit`** on every container (§12.9) so its OOM is contained to
   its own cgroup, never the user session (§12.6 / §12.12 host-safety).
4. **`OOMPolicy=stop`**, exponential-backoff restart, clean-slate rebuild after
   any host incident.
