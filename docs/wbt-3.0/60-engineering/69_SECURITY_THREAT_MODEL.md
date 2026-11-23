---
id: WBT3-890
title: "Security Threat Model"
status: frozen
order: 890
depends_on: [WBT3-150, WBT3-300, WBT3-320]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Security Threat Model

## Assets

- personal durable state
- provider credentials
- WBT API key/session
- camera/microphone/screen content
- external action authority
- operator manifests
- approval state
- traces

## Trust boundaries

1. Browser ↔ WBT backend
2. Browser ↔ realtime provider
3. WBT ↔ model providers
4. WBT ↔ external systems
5. untrusted content ↔ model
6. model intent ↔ Capability Gateway

## Major threats

### XSS steals realtime token
Mitigation:
- ephemeral/scoped tokens
- CSP
- no long-lived secret in JS
- short session duration

### Prompt injection from perceived content
A screenshot/email/webpage may contain malicious instructions.
Treat as data, not authority.

### Client fabricates model tool call
Capability intent is not trusted proof.
Server policy always applies.

### Confused deputy
Operator allowed to read repo A must not use user's interactive grant to write repo B.

Approval bound to exact target/arguments.

### Media overcollection
Explicit UI indicator and default non-retention.

### Autonomous escalation
No operator can grant itself new capabilities or lower policy.

### Trace secret leakage
Redact/cap; do not store raw prompts/media indiscriminately.

## Fail closed areas

- external writes
- destructive actions
- unknown capability
- invalid approval
- unclassifiable action

## Fail degraded areas

- optional model provider
- optional camera
- optional world adapter
- background analytics
