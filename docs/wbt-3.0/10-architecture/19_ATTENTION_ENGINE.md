---
id: WBT3-190
title: "Attention Engine"
status: frozen
order: 190
depends_on: [WBT3-170, WBT3-180]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Attention Engine

## Question

Not "did an event happen?"

Instead:

> Does this deserve user attention or autonomous analysis now?

## Outcomes

- `IGNORE`
- `REMEMBER_EPHEMERAL`
- `QUEUE`
- `NOTIFY`
- `INTERRUPT`
- `PROPOSE_RUN`

Do not use `ACT` as a raw attention result. Action still goes through Runtime/Policy.

## Scoring dimensions

- personal relevance
- urgency
- impact
- novelty
- confidence
- actionability
- repetition/noise
- current user activity
- due time
- operator objective match

## Architecture

Deterministic filters first:

```text
dedup
→ suppress known noise
→ hard urgency rules
→ correlation
→ optional small-model classifier
→ threshold decision
```

Frontier model only for ambiguous, high-value cases.

## Anti-noise requirements

- notification cooldown
- correlation grouping
- collapse repeated same-root-cause events
- escalation as deadline/impact rises
- user feedback ("not relevant") adjusts rules via proposals/evals, not silent prompt mutation

## Example

CI fail on abandoned branch:
`QUEUE` or `IGNORE`

Repeated production failure:
`INTERRUPT + PROPOSE_RUN`

Calendar meeting in 2 minutes:
`NOTIFY`

One transient CPU spike:
`IGNORE`
