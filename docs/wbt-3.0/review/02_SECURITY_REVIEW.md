---
id: WBT3-Q02
title: "Security Review"
status: reviewed
order: 1102
depends_on: []
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Security Review

## Critical requirements

- master provider keys server-only
- ephemeral realtime credentials
- browser intent not trusted as authority
- capability policy server-side
- target-bound approvals
- high-risk fail closed
- media explicit + visible
- raw media non-persistent by default
- untrusted perceived text cannot become instruction authority
- operator cannot self-expand permissions

## Existing good controls retained

- HTTP auth session cookie design
- redaction discipline
- guard risk classifier
- proposal gate
- bounded context/evidence outputs

## Highest-risk future area

Computer/shell/browser-control capabilities.

They must be introduced only after Capability/Policy/Trace is operational.

## Verdict
PASS with required controls encoded as locked decisions.
