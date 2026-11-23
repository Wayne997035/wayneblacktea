---
id: WBT3-140
title: "Capability Gateway"
status: frozen
order: 140
depends_on: [WBT3-110, WBT3-130]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Capability Gateway

## Purpose

A model can suggest an action.
Only WBT Capability Gateway can execute an action.

## Contract

```go
type Capability interface {
    Spec() Spec
    Execute(context.Context, Request) (Result, error)
}

type Spec struct {
    Name             string
    Version          string
    EffectClass      EffectClass
    RiskTier         risk.Tier
    Timeout          time.Duration
    Idempotency      IdempotencyClass
    RequiredScopes   []string
    VerificationMode VerificationMode
}
```

## Effect classes

- `READ`
- `COMPUTE`
- `WRITE_REVERSIBLE`
- `WRITE_EXTERNAL`
- `DESTRUCTIVE`

This is semantic classification.
Risk tier remains numeric/conservative.

## Execution path

```text
resolve
→ schema validate
→ normalize target
→ classify effect/risk
→ policy authorize
→ reserve idempotency key
→ execute
→ reconcile ambiguous timeout
→ verify
→ trace
→ return evidence
```

## Capability sources

- WBT domain services
- existing MCP tools via adapter
- GitHub connector/tool
- Calendar
- Email
- browser/web
- shell
- future device services

Do not make MCP the only capability protocol.

## Realtime clients

Expose only function declarations that produce a server-side `CapabilityIntent`.

The browser MUST NOT directly implement:
- shell
- database write
- send mail
- merge
- production mutation

## Retry

Read operation: often retry-safe.
External write: retry only with explicit idempotency/reconciliation support.
Destructive: never blind retry.
