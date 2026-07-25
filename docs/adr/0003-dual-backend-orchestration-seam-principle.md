---
status: accepted
---

# Dual-backend orchestration seam: a bounded, reusable exception to "no Service/Repository split"

[ADR 0002](0002-gtd-dual-backend-transaction-orchestration.md) opened a **GTD-only** narrow exception to `CLAUDE.md`'s "no Service/Repository split" rule: `BeginTask`/`DeleteTask` were duplicated in full across the pgx/Postgres and database/sql/SQLite stores, and "a bug fixed in one, missed in the other" is a real correctness risk because SQLite is a genuine production backend, not a test stand-in.

A 2026-07-24 full-repo architecture scan plus 2026-07-25 in-code verification found that this shape **recurs**: proposal acceptance (`internal/handler/proposal_handler.go` + `internal/mcp/tools_proposal.go` + `internal/storage/server_stores.go:42-56`, where the code's own comments already name the target as "a TxCoordinator inside the storage package, not pgx leaking into MCP"), knowledge `AddItem` dedup policy (`internal/knowledge/store.go` vs `internal/storage/sqlite/knowledge.go`), and — reported but not yet independently verified — work-session lifecycle, completion-candidate rules, and atom graph traversal. That is roughly **five sites**, not dozens.

Opening a fresh ad-hoc ADR per site turns the rule into "no Service/Repository split, except a steadily growing list of exceptions" — death by a thousand exceptions. Overturning the rule entirely is also wrong: for the **many single-backend domains** (`session`, `workspace`, `vision`, …) the rule's premise ("a single owner, so a service layer adds no value") still holds, and a general Service layer there is pure ceremony.

We therefore **generalize ADR 0002's narrow exception into one bounded, reusable principle**, so future dual-backend seams cite this ADR instead of minting a new one.

## The principle

A **dual-backend transactional or policy method** MAY extract a single dialect-agnostic **orchestration seam**, subject to all three of these bounds:

1. **Orchestration-only, never SQL.** The seam owns step sequencing, error wrapping, commit/rollback lifecycle, idempotency-then-gate-then-guarded-write control flow, ordering, and cross-store failure policy. It MUST NOT own SQL text: placeholder syntax (`$1` vs `?1`), timestamp handling (`NOW()` vs a precomputed Go time), native error signals (`pgx.ErrNoRows` vs `RowsAffected()==0`), and dialect functions stay behind a narrow per-method `execer`-style adapter. Each adapter translates its native signal into one shared boolean/result; the orchestration never sees `pgx` or `database/sql` types directly.
2. **Two real production adapters.** There must be ≥2 genuine production backends behind the seam (Postgres **and** SQLite), not one adapter posing as a seam and not a hypothetical future one. `gtd.RequireAssigneeForInProgress` and the ADR-0002 `BeginTask`/`DeleteTask` orchestration are the existing proofs.
3. **Passes the deletion test.** Deleting the seam must force the same policy to be re-duplicated across the adapters — i.e. the seam removes real, already-present duplication, not merely adds a layer that forwards. If deleting it changes nothing structural, it is a shallow layer and is rejected.

## What this does NOT license

- **Single-backend domains keep the red line.** Where one backend has one owner, no service/repository layer — the rule's premise holds and this exception does not apply.
- **No SQL unification.** No ORM, query builder, generic normalized-evidence framework, or lightweight query-abstraction layer. Duplicated-but-readable SQL is a lesser evil than a home-grown SQL generator (same reasoning as ADR 0001's rejection of codegen for MCP arg structs).
- **No generic CRUD repository, no service locator, no global state machine.**
- **Existing per-dialect integration tests stay.** They verify real per-backend SQL correctness, which fake-adapter orchestration tests cannot substitute for; new orchestration-level tests are added *in addition* (ADR 0002's "add, don't replace" call for dual-backend tests — distinct from ADR 0001's "replace, don't layer" call for the MCP seam, which obsoleted implementation-detail tests).

## Consequences

- A qualifying seam no longer needs its own ADR. The PR extracting it cites ADR 0003 and states, in one line, how it meets bounds 1–3 (with the deletion-test evidence).
- **ADR 0002 remains** as the first concrete instance and living proof (GTD `BeginTask`/`DeleteTask`); this ADR generalizes it, it does not supersede it.
- Candidates expected to qualify: `arch-r2` unit **A1** (proposal acceptance) and, pending verification, work-session lifecycle and completion-candidate rules. **A10** (knowledge dedup) is a *policy* seam under the same principle: the dedup decision is orchestration, the FTS/pgvector/cosine retrieval stays native per adapter.
- A seam that cannot meet all three bounds (e.g. only one real adapter, or it would require unifying SQL) is **not** covered here and must stop and return to design — not force-fit under this ADR.
