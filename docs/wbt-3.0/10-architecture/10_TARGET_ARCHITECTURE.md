---
id: WBT3-100
title: "Target Architecture"
status: frozen
order: 100
depends_on: [WBT3-002, WBT3-005]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Target Architecture

```mermaid
flowchart TB
    U["User"]
    WEB["Web / Optional Desktop Client"]
    PS["Presence Session"]
    PERC["Multimodal Perception"]
    EVT["Event Fabric"]
    WORLD["World Model"]
    ATT["Attention Engine"]
    RT["Personal Intelligence Runtime"]
    CTX["Context Compiler"]
    LLM["internal/llm Model Runtime"]
    CAP["Capability Gateway"]
    POL["Policy / Authority"]
    OP["Persistent Operators"]
    TRACE["Trace / Metrics / Cost"]
    KERNEL["Existing WBT Kernel"]
    EXT["External APIs / MCP / GitHub / Calendar / Browser / Systems"]
    MODELS["Cloud / Local Model Providers"]

    U <--> WEB
    WEB <--> PS
    PS --> PERC
    PERC --> EVT
    EVT --> WORLD
    EVT --> ATT
    ATT --> RT
    OP --> RT
    PS --> RT
    RT --> CTX
    CTX --> KERNEL
    WORLD --> CTX
    RT --> LLM
    LLM --> MODELS
    RT --> CAP
    CAP --> POL
    POL --> EXT
    POL --> KERNEL
    RT --> TRACE
    CAP --> TRACE
    EVT --> TRACE
    OP --> TRACE
    RT --> KERNEL
```

## Three planes

### 1. State plane
Existing WBT durable domains + World Model.

### 2. Control plane
Go:
- Runtime
- Operators
- Events
- Attention
- Capabilities
- Policy
- Trace

### 3. Media/inference plane
Replaceable:
- realtime audio
- VLM/image
- speech
- cloud reasoning
- local inference

## Split-plane realtime principle

Low-latency media may travel directly:

```text
Browser ── ephemeral credential ──> Realtime Provider
```

Control and authority remain:

```text
Browser / model intent
        ↓
WBT backend
        ↓
Capability + Policy + Trace
```

The browser never becomes the authority boundary.
