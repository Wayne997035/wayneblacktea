---
id: WBT3-002
title: "Locked Architecture Decisions"
status: frozen
order: 12
depends_on: [WBT3-000, WBT3-001]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Locked Architecture Decisions

These decisions require a new ADR with concrete failure evidence to change.

## D-001 — WBT Kernel owns authoritative personal state

LLM output is speculative compute.

Durable truth remains in WBT domain models.

## D-002 — WBT 3.0 is a collaboration partner

Do not frame product requirements around emotional dependency or simulated relationship mechanics.

## D-003 — Go + TypeScript remain core languages

Go owns server/runtime/control-plane behavior.
TypeScript/React owns interactive UI and browser media/session logic.

Native code in other languages may exist later only behind an adapter boundary and is outside the 3.0 core deliverable.

## D-004 — Evolve, do not duplicate, existing WBT primitives

- `internal/contextpack` → Context Compiler
- `internal/llm` → expanded model/runtime provider layer
- `internal/evals` → 3.0 evals
- existing `scheduler` → time trigger producer; reasoning migrates toward Operators
- `worksession` → durable human/work lifecycle, not an AI turn executor
- `guard` risk knowledge → reusable risk taxonomy, but autonomous policy semantics must be fail-closed for high-risk operations

## D-005 — Runtime Run is not Work Session

A `work_session` may contain many runtime runs.

An operator run may exist without a work session.

A realtime conversation may create several runtime runs.

Therefore Runtime needs a distinct execution identity linked optionally to `work_session_id`.

## D-006 — All side effects pass Capability Gateway

No handler, realtime client, operator, model adapter or prompt may bypass it for autonomous action.

## D-007 — High-risk autonomous policy fails closed

Existing guard event persistence may fail open because it is observational tooling.
3.0 action authorization may not inherit that behavior.

If required policy/approval state cannot be verified, external/destructive action is denied.

## D-008 — Direct browser/provider realtime uses short-lived credentials

Never ship long-lived provider keys to the browser.

## D-009 — Browser tool calls are intents, not authority

A realtime model/client may request an action.
Server-side WBT must authorize and execute it.

## D-010 — World Model is separate from Memory

Memory answers "what have we learned / decided / experienced?"
World Model answers "what do we currently believe is true now?"

## D-011 — Observation and inference are separate

`observed`, `user_asserted`, `derived`, `inferred` must not collapse into one truth class.

## D-012 — Raw camera/screen media is non-persistent by default

Persist metadata/evidence digests only unless the user explicitly saves an artifact.

## D-013 — Attention does not directly bypass action policy

Attention outcome can request/propose a Runtime run.
It cannot directly perform destructive work.

## D-014 — Personality controls cannot reduce truthfulness or authority disclosure

Adjustable:
- directness
- humor
- verbosity
- initiative
- challenge level

Fixed:
- uncertainty disclosure
- truthfulness
- policy visibility
- approval rules
- safety/risk reporting

## D-015 — 3.0 Web becomes a collaboration surface

Legacy CRUD remains available under Explore, but should not remain the primary information architecture.
