---
id: WBT3-370
title: "Sensor Adapter Contract"
status: frozen
order: 370
depends_on: [WBT3-320]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Sensor Adapter Contract

3.0 defines the boundary for future sensors without requiring platform-specific implementation.

```go
type SensorAdapter interface {
    ID() string
    Capabilities() []SensorCapability
    Observe(context.Context, ObserveRequest) (ObservationBatch, error)
}
```

Potential adapters:
- browser camera
- browser microphone
- browser screen
- Home Assistant
- Bluetooth instrument bridge
- local system telemetry
- future wearable

## Language boundary

3.0 core adapters are Go/TypeScript.

If a future macOS sensor requires Swift/ObjC:
- implement outside core
- expose HTTP/gRPC/socket contract
- do not import native runtime into WBT domain packages

## Trust

Each adapter declares:
- source identity
- authentication
- precision
- expected freshness
- sensitivity
- whether values are direct measurements or derived
