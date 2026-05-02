<p align="center">
  <img src="docs/wayneblacktea.png" alt="wayneblacktea" width="320">
</p>

<p align="center">
  <a href="./README.md"><strong>English</strong></a> &nbsp;·&nbsp; <strong>繁體中文</strong>
</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-8C2A1A.svg" alt="MIT License"></a>
</p>

<p align="center">
  一個給 AI agent 的 personal-OS MCP server — 你的目標、決策、知識、學習
  全都活在同一顆共用的腦裡，讓你合作的 AI 已經知道你的脈絡，而不是
  每次對話都要從零解釋一次。
</p>

---

## 安裝

單一 binary、互動 wizard、預設 SQLite — 不用自己起任何 infra。

```bash
go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest
wbt init    # 問你 CLAUDE_API_KEY、選 SQLite 或 Postgres、寫 .env 跟 .mcp.json
```

然後把 MCP server 註冊給 Claude Code：

```bash
claude mcp add --scope user wayneblacktea -- wbt mcp
```

完 — 單一 binary、單一指令。`wbt mcp` 是 MCP stdio entry；`wbt serve`（選用）跑 dashboard HTTP API，想要 web UI 才開。

## 你會得到

Claude Code 連上 `wbt mcp` 後，所有支援 MCP 的 agent 都讀寫同一份儲存：

| Context | 擁有什麼 |
|---|---|
| **GTD** | 目標 → 專案 → 任務（含重要性與討論脈絡），加 activity log |
| **Decisions** | 架構與設計決策，含理由與替代方案，可依 repo 查詢 |
| **Knowledge** | 文章、TIL、書籤、Zettelkasten 筆記 — 全文與 pgvector 語意搜尋 |
| **Learning** | 間隔重複概念卡，跑 FSRS 排程，可從新存的知識自動提案 |
| **Sessions** | 跨 session 的交接筆記 — 「下次要繼續什麼」 |
| **Proposals** | Agent 原創、等使用者確認的物件 |
| **Workspace** | 追蹤的 Git repo，含狀態、已知問題、下一步計畫 |

## 自動記憶（不用你提醒）

Agent 不需要記得呼叫工具，server 會自動接住：

- **MCP middleware classifier** — 任何 significant tool 呼叫（`complete_task`、`confirm_proposal`、`upsert_project_arch`、`update_project_status`、`resolve_handoff`、`sync_repo`）執行成功後丟給 Haiku 非同步分類；隱性決策自動 log_decision、隱性任務自動 add_task。每分鐘 60 次 rate cap、dedup、prompt injection boundary。
- **Stop hook**（`wbt-doctor`）— Claude Code session 結束時 transcript 壓成 ≤500 字 summary，同時寫進 `session_handoffs.summary_text` 跟可搜尋的 `knowledge_items`。
- **SessionStart hook**（`wbt-context`）— 下次 session 開啟時自動把上次 handoff、最近決策、今天 due reviews 注入成 context。
- **Saturday reflection cron** — 週六批次掃 7 天 activity_log + decisions，叫 Haiku 起草 3-5 條 retrospective knowledge，走 `pending_proposals` 等你確認。
- **Auto-consolidation** — 同 actor 30 天內 ≥5 條相關 activity 被合併成一條 knowledge proposal。

## 設計哲學

**結構優先於 prompt。** 把你想要 AI 記得的部分編碼成明確 schema。Agent 之間沒漂移、沒有「我記得你提過…」，就是資料。

**使用者保留決定權。** Agent 提案，你確認。摩擦本身就是重點 — 一個替你做決定的系統最後會讓你不會做決定。

**讓遺忘可見。** 再自律的 agent 都會忘記收尾。Server 把每次工具呼叫都記下來，把模式講出來 — 卡住的 in-progress 任務、累積的 pending 提案、登錄了決策卻沒做 session 開頭 recall。

**Workflow 工具，不是原始 CRUD。** Agent 接觸面提供「拿今天的 context」、「確認一個計畫」、「登錄一個決策」這種動詞操作。規則住在工具層，而不是散落在每個 client 的 prompt 裡。

## 這 *不是* 什麼

- **不是團隊產品。** 一個人，多個 agent。沒有 RBAC，沒有共享 workspace，沒有 Notion-clone 協作。
- **不是 hosted 服務。** Self-host 在你自己機器上。Workspace scope 只是幫你資料隔離，不是多租戶。
- **不是穩定 API。** 一個人開發跟維運。release 不規律、會有 breaking change、儀表板還有粗糙的角落。
- **不是有記憶的 chatbot。** Schema 才是記憶，對話歷史不重要。

---

採用 [MIT](./LICENSE) 授權。架構細節在 [`docs/architecture.md`](./docs/architecture.md)。
