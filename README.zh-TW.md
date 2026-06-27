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

```bash
go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest
wbt setup
```

就這樣。`wbt setup` 一條龍：建立 SQLite 目錄、解析 port、佔用就 reclaim、用 `nohup` 在背景啟動 `wayneblacktea-server`（PID file 寫到 `$XDG_STATE_HOME/wayneblacktea/`）、輪詢 `/health`，最後用 `claude mcp add --transport http` 把 HTTP MCP 註冊到 Claude Code。

```
$ wbt setup
==> Reading or creating config…          [ok] Config ready
==> Ensuring SQLite directory…           [ok] SQLite directory ready
==> Resolving port…                      [ok] Port resolved: 8420
==> Checking for an existing healthy server…
==> Reclaiming TCP port if occupied…     [ok] Port is free
==> Spawning wayneblacktea-server in the background…
                                         [ok] Server spawned (pid 12345, logs ~/.local/state/wayneblacktea/server.log)
==> Waiting for /health…                 [ok] Server is healthy
==> Registering MCP with Claude Code…    [ok] claude mcp add wayneblacktea --transport http http://127.0.0.1:8420/mcp
All set. wayneblacktea is running at http://127.0.0.1:8420
```

從任何目錄開 Claude Code 即可使用，不用 `.mcp.json`。

姊妹指令：

| 指令 | 用途 |
|------|------|
| `wbt status [--format json\|plain]` | 看背景 server 有沒有跑、健康狀態 |
| `wbt stop` | 停掉背景 server（idempotent，沒跑會回報 not running） |
| `wbt restart` | `stop` + `setup` |
| `wbt setup --port 9090` | 用非預設 port |
| `wbt setup --no-mcp` | 跳過 `claude mcp add` 步驟 |
| `wbt init` | `wbt setup` 的 deprecated alias |

核心 MCP 記憶功能不需要 Anthropic API key。Postgres、Docker、Railway 部署方式見 [`docs/install.md`](./docs/install.md)，常見問題見 [Troubleshooting](./docs/install.md#troubleshooting)。

### 安裝管道

| 管道 | 指令 | 適合 |
|------|------|------|
| Homebrew | `brew install --cask Wayne997035/tap/wayneblacktea-cli && wbt setup` | macOS 使用者，要 `wbt` 進 PATH 並自動升級 |
| DXT | 從 [release](https://github.com/Wayne997035/wayneblacktea/releases) 下載 `wayneblacktea.dxt`，在 Claude Desktop 開啟（需 `wbt` 已在 PATH） | Claude Desktop 一鍵安裝 |
| curl \| bash | `curl -fsSL https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.sh \| bash` | 不用 Homebrew / Go，要 cosign 驗證的 binary |
| go install | `go install github.com/Wayne997035/wayneblacktea/cmd/wbt@latest && wbt setup` | Go 開發者，從原始碼安裝 |

Postgres、Docker、Railway 部署方式見 [`docs/install.md`](./docs/install.md)，常見問題見 [Troubleshooting](./docs/install.md#troubleshooting)。

## 5 分鐘 onboarding

`wbt setup` 完成後，從任何目錄開 Claude Code，試試：

```
# 看看 server 現在記得什麼
> get_today_context

# 記錄一個決策（永久儲存，可依 repo 查詢）
> log_decision "選 SQLite 是因為..."

# 加任務
> add_task "實作登入流程" --project my-project

# 原子確認多步計畫
> confirm_plan {phases: [...], decisions: [...]}

# 加一條知識筆記（支援關鍵字 + semantic vector 搜尋）
> add_knowledge {title: "JWT 過期最佳實踐", content: "..."}

# 記錄現在還不能做的想法
> add_vision_item {title: "多 agent 協作協定", why_blocked: "要等 E1 provider interface 完成"}
```

完整工具列表：[`docs/mcp-tools.md`](./docs/mcp-tools.md)。

## 架構

```mermaid
flowchart TD
    CC["Claude Code\n(wbt mcp — stdio)"]
    CURL["HTTP 客戶端\n(dashboard / curl)"]
    HTTPMCP["HTTP MCP transport\n(/mcp)"]

    CC   -->|MCP stdio|        SRV
    CURL -->|REST /api/star|   SRV
    HTTPMCP -->|MCP over HTTP| SRV

    SRV["wayneblacktea-server\n(Echo HTTP + mcp-go)"]

    SRV --> STORES["Store interfaces\ngtd · decision · knowledge · session\nproposal · vision · learning · workspace"]

    STORES --> SQLITE["SQLite\n(本機開發，零 infra)"]
    STORES --> PG["Postgres + pgvector\n(Aiven / Railway)"]

    SRV --> AI["AI providers\nAnthropic · Gemini embeddings\nGroq (Discord bot)"]
    SRV --> SCHED["Scheduler\n週六 reflection\nauto-consolidation · decay prune"]
    SRV --> DISCORD["Discord bot"]
```

同一個 process 同時服務 MCP stdio、HTTP REST、HTTP MCP — 不用跑多個元件。

## 你會得到

Claude Code 連上 `wbt mcp` 後，所有支援 MCP 的 agent 都讀寫同一份儲存：

| Context | 擁有什麼 |
|---------|---------|
| **GTD** | 目標 → 專案 → 任務（含重要性與討論脈絡），加 activity log |
| **Decisions** | 架構與設計決策，含理由與替代方案，可依 repo 查詢 |
| **Knowledge** | 文章、TIL、書籤、筆記 — 全文與 pgvector 語意搜尋；Markdown heading 自動 fan-out 成可獨立搜尋的子節點 |
| **Learning** | 間隔重複概念卡，跑 FSRS 排程，可從新存的知識自動提案 |
| **Sessions** | 跨 session 的交接筆記 — 「下次要繼續什麼」 |
| **Proposals** | Agent 原創、等使用者確認的物件 |
| **Vision** | 還沒準備好成為 task 的想法 — `open → discussing → maturing → promoted → dismissed` |
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

## 與類似工具比較

| | wayneblacktea | claude-mem | Hermes Agent Memory |
|---|---|---|---|
| **範圍** | 完整 personal OS（GTD + 決策 + 知識 + 學習 + 願景） | 對話記憶 | 對話 + entity 記憶 |
| **儲存** | SQLite（本機）或 Postgres+pgvector（雲端） | 託管雲端 | 託管雲端 |
| **掌控** | Self-hosted，資料自有 | 第三方 | 第三方 |
| **提案閘門** | Agent 提案 → 使用者確認 | 無 | 無 |
| **間隔重複** | FSRS 學習卡 | 無 | 無 |
| **儀表板** | Web UI + Discord bot | 無 | 無 |
| **取捨** | 設定較多，功能面更廣 | 零設定 | 零設定 |

## 驗證 release binary

Release binary 用 [cosign](https://docs.sigstore.dev/cosign/overview/) keyless 簽章（GitHub OIDC）。下載後驗證：

```bash
# 安裝 cosign：https://docs.sigstore.dev/cosign/system_config/installation/

cosign verify-blob \
  --certificate-identity-regexp "https://github.com/Wayne997035/wayneblacktea/.github/workflows/release.yml@refs/tags/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --signature wayneblacktea_checksums.txt.sig \
  --certificate wayneblacktea_checksums.txt.pem \
  wayneblacktea_checksums.txt
```

`.sig` 和 `.pem` 檔案隨每個 GitHub Release 的 binary 一起附上。

## 這 *不是* 什麼

- **不是團隊產品。** 一個人，多個 agent。沒有 RBAC，沒有共享 workspace，沒有 Notion-clone 協作。
- **不是 hosted 服務。** Self-host 在你自己機器上。Workspace scope 只是幫你資料隔離，不是多租戶。
- **不是穩定 API。** 一個人開發跟維運。release 不規律、會有 breaking change、儀表板還有粗糙的角落。
- **不是有記憶的 chatbot。** Schema 才是記憶，對話歷史不重要。

---

採用 [MIT](./LICENSE) 授權。架構細節在 [`docs/architecture.md`](./docs/architecture.md)。
