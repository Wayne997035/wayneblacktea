---
id: WBT3-320
title: "Multimodal Perception"
status: frozen
order: 320
depends_on: [WBT3-300]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Multimodal Perception

## Sources

- uploaded image
- camera frame
- screen frame
- OCR/text extraction
- barcode/QR helper
- audio transcription/classification
- future external sensors

## Portable baseline

Do not require native continuous-video support from every model.

Normalize visual input as a sequence of frames/observations.

This matters because providers differ:
- some realtime models accept image but not video
- some live APIs accept continuous frames but have session constraints

## Perception output

```go
type PerceptionResult struct {
    Observations []Observation
    Entities     []EntityCandidate
    Text         []TextCandidate
    Uncertainty  []Uncertainty
    SuggestedNextObservations []ObservationRequest
}
```

## Perception is evidence generation

The VLM should not directly mutate:
- knowledge
- decisions
- tasks
- authoritative World state

It returns candidates/evidence.

## Provider composition

A single frame may use:
- VLM
- OCR
- barcode
- deterministic metadata

Fusion occurs server-side.

## Cost control

Use:
- frame sampling
- scene-change detection
- manual snapshot for detail
- low resolution by default
- high resolution only on request
