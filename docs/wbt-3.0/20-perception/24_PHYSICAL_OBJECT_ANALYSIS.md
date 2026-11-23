---
id: WBT3-340
title: "Camera, Image and Physical Object Analysis"
status: frozen
order: 340
depends_on: [WBT3-320, WBT3-330]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Camera, Image and Physical Object Analysis

## Required 3.0 demo class

User presents an object.

WBT should:
1. recognize object category/candidates
2. read visible labels when possible
3. expose confidence
4. ask for missing views if needed
5. optionally use external lookup
6. combine with personal/project context
7. provide a useful next action

## Identification hierarchy

Prefer strong evidence:

```text
barcode / QR / exact serial
> clearly read model label
> distinctive visual model match
> generic category visual inference
```

## Object candidate

```text
category
brand_candidate
model_candidate
visible_text
identifiers
confidence
evidence_frames
```

## Persistence

Default:
- ephemeral candidate attached to Presence Session

Optional:
- promote to World Entity
- save artifact/knowledge when user requests

## Example

Do not answer only:

> "This is an SSD."

If context supports it, answer:

> "This appears to be a Samsung 990 PRO 2TB. The model text is visible with high confidence. If your question is whether it fits the currently tracked workstation, I can check the motherboard notes."

The second part is why WBT is a personal intelligence system rather than generic vision.
