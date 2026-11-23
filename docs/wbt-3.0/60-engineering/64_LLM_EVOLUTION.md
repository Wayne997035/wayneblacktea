---
id: WBT3-840
title: "internal/llm Evolution Plan"
status: frozen
order: 840
depends_on: [WBT3-130, WBT3-810]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# `internal/llm` Evolution Plan

## Compatibility

Do not break current:
- `JSONRequest`
- `JSONClient`
- `Chain`
- current callers

## New layers

Suggested files:

```text
internal/llm/generate.go
internal/llm/capabilities.go
internal/llm/router.go
internal/llm/realtime.go
internal/llm/usage.go
```

Provider adapters can implement only supported interfaces.

## Generation result

Must normalize:
- text
- tool intents
- usage
- provider/model
- finish reason
- latency
- error class

## Realtime provider

Backend adapter's main job can be bootstrap/credential/configuration rather than proxying every media packet.

## Cloud/local

Local model servers remain external endpoints.
WBT does not own GPU lifecycle.

## Provider health

Router gets health signals.
Existing chain fallback semantics inform, but do not fully define, multimodal routing.
