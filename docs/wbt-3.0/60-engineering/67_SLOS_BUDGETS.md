---
id: WBT3-870
title: "SLOs and Budgets"
status: frozen
order: 870
depends_on: [WBT3-110, WBT3-300]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# SLOs and Budgets

These are engineering targets, not guarantees of external provider latency.

## Presence UX targets

- local UI state update: p95 < 100 ms after event received
- user interrupt → playback stop: target < 250 ms
- text send → visible activity: < 150 ms
- realtime first audio: target p50 < 1.5 s, p95 < 3 s where provider/network allow
- read-only local WBT capability: p95 < 500 ms
- quick image identification: target < 3 s
- deep analysis: show progress state; no fake ETA

## Runtime default budgets

Interactive:
- max turns 12
- max tool calls 20
- max duration 120 s
- configurable cost ceiling

Operator:
- lower per-run cost
- strict concurrency
- rate limits
- backoff

## Perception

Default camera:
- low/medium resolution
- adaptive <= 1 fps for model submission
- manual high-res snapshot for labels/details

No unbounded video upload.
