---
id: WBT3-INDEX
title: "WBT 3.0 — E.V.-class Personal Intelligence Formal Specification"
status: frozen
order: 0
depends_on: []
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# WBT 3.0 — Formal Specification

這套文件是 `wayneblacktea` 3.0 的正式 architecture baseline。

## Product definition

**WBT 3.0 = AI Collaboration Partner / Personal Intelligence System**

它不是情感依賴型產品，也不是單純 chatbot、memory server 或 OpenJarvis clone。

3.0 的最低完整形態：

```text
Talks
Sees
Remembers
Observes
Reasons
Acts
Verifies
Learns
Proactively assists
```

E.V. 是主要 behavior north star；OpenJarvis 是 agent/runtime 參考；TARS/CASE 是 operational interaction 參考。三者都只是 reference，不是要複製其 fiction / codebase / personality。

## Core implementation boundary

```text
WBT Core / Runtime: Go
WBT Web / UI:       TypeScript + React
Optional Desktop:   Go + React/TypeScript (e.g. Wails) only if the feature can stay in this language boundary
External compute:   API / service / process boundary
```

若某個 macOS 深度 sensor 能力必須額外使用 Swift/ObjC 才合理，該能力 **不屬於 3.0 core package 的必交付範圍**。3.0 只定義 adapter contract。

## How to read this specification

這套 spec 不是每次都要把 70+ 份文件全部塞給 AI。

正確方式是 **先讀全域約束，再依目前 implementation phase 讀對應 subsystem spec**。

### A. 第一次接手 WBT 3.0 的 AI / 架構師

第一次進入 3.0 工作時，依序讀：

1. `README.md`
   - 了解產品目標、文件權威順序與 implementation dependency。

2. `00-foundation/00_PRODUCT_CHARTER.md`
   - 確認 WBT 3.0 是 **AI Collaboration Partner / Personal Intelligence System**。
   - E.V. 是 behavior north star；OpenJarvis 與 TARS/CASE 是參考，不是照抄目標。

3. `00-foundation/01_SCOPE_NON_GOALS.md`
   - 確認 3.0 必做與明確不做的事項。

4. `00-foundation/02_LOCKED_DECISIONS.md`
   - **最重要的 architecture constraints。**
   - 在沒有新 ADR 與具體 failure evidence 前不得自行推翻。

5. `00-foundation/03_CURRENT_STATE_AUDIT.md`
   - 了解現在 WBT 已經有什麼。
   - 防止重造 `contextpack`、`internal/llm`、`internal/evals`、`worksession`、scheduler、guard 等既有能力。

6. `00-foundation/04_TERMINOLOGY_AND_SESSION_MODEL.md`
   - 先分清：
     - `session_handoff`
     - `work_session`
     - `presence_session`
     - `runtime_run`
     - `operator`

7. `00-foundation/05_CURRENT_COMPONENT_DISPOSITION.md`
   - 看哪些 existing package 是 KEEP / EVOLVE / NEW。

8. `10-architecture/10_TARGET_ARCHITECTURE.md`
   - 建立整體 architecture mental model。

9. `60-engineering/69_SECURITY_THREAT_MODEL.md`
   - 在開始實作任何 Runtime / Capability / Perception 前先讀安全邊界。

10. `70-delivery/70_PHASE_PLAN.md`
    - 確認目前應該做哪個 Phase。

11. `70-delivery/75_AI_CODING_AGENT_GUIDE.md`
    - coding agent 必遵守的 repo / implementation 規則。

12. `70-delivery/76_DEFINITION_OF_DONE.md`
    - 知道 3.0 什麼時候才算真的完成。

---

### B. 實作某個 Phase 時

完成上面的第一次全域閱讀後，**不要每次全部重讀**。

依 Phase 讀：

#### Phase 1 — Runtime + Text Presence

必讀：

- `10-architecture/11_RUNTIME.md`
- `10-architecture/12_CONTEXT_COMPILER.md`
- `10-architecture/13_MODEL_RUNTIME.md`
- `10-architecture/20_TRACE_OBSERVABILITY.md`
- `10-architecture/21_WORKSESSION_INTEGRATION.md`
- `20-perception/20_PRESENCE_SESSION.md`
- `60-engineering/60_PACKAGE_OWNERSHIP.md`
- `60-engineering/61_EXISTING_EVOLUTION.md`
- `60-engineering/64_LLM_EVOLUTION.md`
- `60-engineering/65_EVAL_EVOLUTION.md`
- `60-engineering/68_TEST_STRATEGY.md`

#### Phase 2 — Capability + Policy

必讀：

- `10-architecture/14_CAPABILITY_GATEWAY.md`
- `10-architecture/15_POLICY_AUTHORITY.md`
- `60-engineering/63_GUARD_RISK_INTEGRATION.md`
- `60-engineering/66_FAILURE_MODES.md`
- `60-engineering/69_SECURITY_THREAT_MODEL.md`

#### Phase 3 — Web 3.0 Shell

必讀：

- `40-web/40_WEB_INFORMATION_ARCHITECTURE.md`
- `40-web/41_PRESENCE_SURFACE.md`
- `40-web/42_WORLD_SURFACE.md`
- `40-web/43_MIND_SURFACE.md`
- `40-web/44_OPERATIONS_SURFACE.md`
- `40-web/45_CONTROL_SURFACE.md`
- `40-web/46_EXPLORE_MIGRATION.md`
- `40-web/47_VISUAL_DESIGN_SYSTEM.md`
- `40-web/48_MOTION_STATE.md`
- `40-web/49_ACCESSIBILITY_PERFORMANCE.md`

#### Phase 4 — Realtime Voice

必讀：

- `20-perception/20_PRESENCE_SESSION.md`
- `20-perception/21_REALTIME_VOICE.md`
- `50-data-api/54_REALTIME_CONTROL_PROTOCOL.md`
- `50-data-api/55_EPHEMERAL_CREDENTIALS.md`
- `50-data-api/56_RETENTION_PRIVACY.md`

#### Phase 5 — Image / Camera / Screen / Physical Objects

必讀：

- `20-perception/22_MULTIMODAL_PERCEPTION.md`
- `20-perception/23_ACTIVE_PERCEPTION.md`
- `20-perception/24_PHYSICAL_OBJECT_ANALYSIS.md`
- `20-perception/25_SCREEN_PERCEPTION.md`
- `20-perception/26_PERCEPTION_CONFIDENCE.md`
- `20-perception/27_SENSOR_ADAPTER_CONTRACT.md`

#### Phase 6 — Event + World + Attention

必讀：

- `10-architecture/17_EVENT_FABRIC.md`
- `10-architecture/18_WORLD_MODEL.md`
- `10-architecture/19_ATTENTION_ENGINE.md`
- `50-data-api/51_EVENT_WORLD_SCHEMA.md`
- `30-interaction/32_PROACTIVITY_INTERRUPTION.md`

#### Phase 7 — Persistent Operators

必讀：

- `10-architecture/16_OPERATOR_SYSTEM.md`
- `60-engineering/62_SCHEDULER_OPERATOR_MIGRATION.md`
- `40-web/44_OPERATIONS_SURFACE.md`

#### Phase 8 — Hardening / Release

必讀：

- `60-engineering/65_EVAL_EVOLUTION.md`
- `60-engineering/66_FAILURE_MODES.md`
- `60-engineering/67_SLOS_BUDGETS.md`
- `60-engineering/68_TEST_STRATEGY.md`
- `60-engineering/69_SECURITY_THREAT_MODEL.md`
- `70-delivery/71_RELEASE_GATES.md`
- `70-delivery/72_ACCEPTANCE_DEMOS.md`
- `70-delivery/74_ROLLBACK_MIGRATION.md`
- `70-delivery/76_DEFINITION_OF_DONE.md`

---

### C. 只想審查整體架構

如果是 reviewer / architecture review，不準備改 code，建議順序：

1. `README.md`
2. `00-foundation/00_PRODUCT_CHARTER.md`
3. `00-foundation/02_LOCKED_DECISIONS.md`
4. `00-foundation/03_CURRENT_STATE_AUDIT.md`
5. `10-architecture/10_TARGET_ARCHITECTURE.md`
6. `70-delivery/71_RELEASE_GATES.md`
7. `FINAL_REVIEW_SUMMARY.md`
8. `MACHINE_QA.md`

再依 reviewer 專業領域進入：

- Security → `60-engineering/69_SECURITY_THREAT_MODEL.md`
- Backend → `10-architecture/*`
- Data → `50-data-api/*`
- Realtime/Perception → `20-perception/*`
- Web/UX → `40-web/*`
- Delivery → `70-delivery/*`

---

### D. 給 Coding Agent 的最短啟動指令

可以直接要求：

> Read `README.md`, then the locked decisions, current-state audit, target architecture, security threat model, current phase spec, AI coding agent guide, and Definition of Done. Inspect the existing repository implementation before proposing new packages. Do not duplicate existing WBT subsystems. Report any conflict with a locked decision before changing architecture.

---

## Document authority order

如果文件之間有衝突，依這個順序判定：

1. `architecture-contract.json`
2. `decision-register.json`
3. `00-foundation/02_LOCKED_DECISIONS.md`
4. 對應 subsystem spec
5. `70-delivery/*`
6. `references/*`

`FINAL_REVIEW_SUMMARY.md` 與 `MACHINE_QA.md` 是審查結果，不凌駕 architecture contract。

The existing repo's root AGENTS.md, current architecture, migration immutability rules, dual-backend parity and proposal-gated durable writes remain hard constraints unless this pack explicitly supersedes a product-level 2.0 non-goal.

## Primary dependency order

```text
Existing WBT Kernel
        │
        ▼
Runtime
        │
        ├── Context Compiler (evolve contextpack)
        ├── Model Runtime (evolve internal/llm)
        ├── Capability Gateway
        │       └── Policy / Authority
        └── Trace
                │
                ▼
Presence Session / Web Collaboration Surface
                │
                ├── Realtime Voice
                ├── Image / Camera / Screen Perception
                └── Active Perception
                │
                ▼
Event Fabric
        │
        ├── World Model
        └── Attention Engine
                │
                ▼
Persistent Operators
                │
                ▼
Closed-loop Action → Verification → Outcome → Learning
```

## Important supersession

`docs/internal/wayneblacktea-2.0-development-prompt.md` previously said WBT 2.0 was *not* a new autonomous agent framework. WBT 3.0 **intentionally changes that product target** by adding a native Personal Intelligence Runtime.

This does **not** invalidate these 2.0 principles:

- structure beats prompts
- proposal-gated AI-originated durable writes
- deterministic context assembly
- verified outcome learning
- no FK constraints
- Postgres / SQLite parity
- bounded resource use
- deterministic evals
