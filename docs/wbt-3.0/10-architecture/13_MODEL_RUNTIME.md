---
id: WBT3-130
title: "Model Runtime — evolve internal/llm"
status: frozen
order: 130
depends_on: [WBT3-110]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Model Runtime — evolve `internal/llm`

## Do not create a second model subsystem

Existing `internal/llm.JSONClient` and `Chain` stay supported.

## Add capability-oriented interfaces

Avoid one giant interface.

```go
type GenerateClient interface {
    Name() string
    Capabilities() Capabilities
    Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}

type RealtimeProvider interface {
    Name() string
    CreateClientSession(ctx context.Context, req RealtimeBootstrap) (RealtimeCredential, error)
}

type Capabilities struct {
    Text        bool
    ImageInput  bool
    AudioInput  bool
    AudioOutput bool
    ToolCalling bool
    Realtime    bool
    MaxContext  int
}
```

Existing `JSONClient` remains the small deterministic structured-task API.

## Routing

Routing inputs:
- task class
- latency target
- modality
- privacy/sensitivity
- tool calling requirement
- cost budget
- context size
- provider health
- user/provider preference

Start deterministic.

## Multi-model architecture

Realtime voice model is not always the deepest reasoning model.

Recommended pattern:

```text
Realtime model
   │
   ├─ simple conversation / clarification
   └─ delegate_analysis(...)
          ↓
       Runtime
          ↓
       frontier/deep model
          ↓
       structured result
          ↓
       realtime model speaks result
```

## Existing Chain

Keep Chain for:
- classification
- summarization
- deterministic JSON feature tasks

Do not force realtime routing into a simple failover Chain abstraction.

## Provider keys

Long-lived keys are server-only.

Browser-facing realtime sessions receive scoped, short-lived credentials when supported.
