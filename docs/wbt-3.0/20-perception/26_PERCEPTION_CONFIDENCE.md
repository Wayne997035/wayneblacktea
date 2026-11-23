---
id: WBT3-360
title: "Perception Confidence and Evidence"
status: frozen
order: 360
depends_on: [WBT3-320]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Perception Confidence and Evidence

## Evidence classes

- `sensor_observed`
- `api_observed`
- `user_asserted`
- `model_extracted`
- `model_inferred`
- `derived_deterministic`

## Confidence is not enough

Store:
- confidence
- evidence class
- source
- timestamp
- validity
- contradictory evidence

## Language contract

High confidence observation:
> "The label reads MZ-V9P2T0."

Inference:
> "That model number corresponds to a Samsung 990 PRO 2TB."

Weak inference:
> "It looks like an NVMe SSD, but I can't read the model yet."

## Conflict

When OCR and visual inference disagree:
- do not silently select one
- request better observation or use stronger identifier

## World update thresholds

Sensor/API exact state can update current World state automatically under source policy.

VLM-inferred identity should usually remain a candidate until high confidence or user confirmation.
