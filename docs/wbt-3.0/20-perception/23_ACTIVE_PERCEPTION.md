---
id: WBT3-330
title: "Active Perception"
status: frozen
order: 330
depends_on: [WBT3-320, WBT3-180]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Active Perception

## Definition

When the current observation cannot answer the task confidently, WBT should seek the missing evidence.

Examples:
- "turn it around"
- "move closer to the label"
- "show the connector"
- "focus on the error LED"
- "share the terminal window"
- "measure the voltage and tell me the value"

## Decision

```text
current evidence
→ sufficient?
  ├─ yes → answer/act
  └─ no
      → can another observation reduce uncertainty?
          ├─ yes → request observation
          └─ no → state limitation
```

## Observation request type

```go
type ObservationRequest struct {
    Modality   string
    Target     string
    Instruction string
    Reason     string
    ExpectedInformation []string
}
```

## Critical rule

Never convert a lack of sensor evidence into confident physical claims.

Examples:
- camera cannot prove metal purity
- camera cannot prove internal contamination
- image alone cannot guarantee a medicine is authentic

WBT should specify what additional measurement/tool is required.

## User friction

Ask for another observation only when expected information gain is meaningful.
Do not create an endless "show me another angle" loop.
Use a max observation-request budget per task.
