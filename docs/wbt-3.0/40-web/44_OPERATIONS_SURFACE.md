---
id: WBT3-540
title: "Operations Surface"
status: frozen
order: 540
depends_on: [WBT3-160, WBT3-200, WBT3-500]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Operations Surface

## Purpose

Production control room for AI execution.

## Operator list

Show:
- status
- objective
- triggers
- last/next run
- health
- failure streak
- model/cost budget
- capability grants

## Run detail

Timeline:

```text
trigger
context
model
capability intent
policy
execute
verify
result
```

## Controls

- pause operator
- resume
- run once
- dry run
- disable
- inspect manifest

## Trace filters

- failed only
- external writes
- denied actions
- high cost
- attention interruptions
- realtime runs

## Cost

Reuse current AI cost ledger; surface it alongside run attribution.
