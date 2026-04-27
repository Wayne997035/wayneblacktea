<p align="center">
  <img src="docs/wayneblacktea.png" alt="wayneblacktea" width="320">
</p>

<p align="center">
  <a href="README.md">English</a> | <strong>繁體中文</strong>
</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-8C2A1A.svg" alt="MIT License"></a>
</p>

<p align="center">
  一個給 AI agent 的 personal-OS server — 你的目標、決策、知識、學習
  全都活在同一顆共用的腦裡，讓你合作的 AI 已經知道你的脈絡，而不是
  每次對話都要從零解釋一次。
</p>

---

## 為什麼存在

大多數 AI 工作流程是無狀態的。每次對話從零開始、每個 agent 都健忘、
你整天在重貼連結、重新解釋昨天講過的事。你加越多 agent — 編輯器助
手、Discord 小幫手、每日摘要 — 情況越糟。每一個都產出別人看不到的
東西，最後你變成系統裡唯一一塊記憶體。

wayneblacktea 走相反的路：把你的工作模型化成 **結構化資料** — 目
標、專案、任務、決策、知識條目、概念卡、agent 提案、session 交
接 — 讓每個 agent 都讀寫同一份儲存。當你問編輯器「我昨天在幹嘛？」
它會從 schema 拉出真實答案；當你從 Discord 存一條連結，系統之後
可以從那條連結幫你提案一張間隔重複卡，不用你再打字一次；當你確認
一個計畫，每個 phase 會原子化變成真實任務。

跟你合作的 AI 已經知道你的脈絡，你不再是剪貼簿。

## 這帶來什麼

- **編輯器、Discord、儀表板對齊狀態。** 在 Discord 存一條連結，一
  秒後在儀表板看到。沒有「等等我有跟你講過嗎」。
- **存進來的知識自動餵複習佇列。** 你存一篇文章或一條 TIL，系統
  會幫你起草一張間隔重複概念卡。確認或跳過 — 佇列從你的閱讀習慣
  自然成長，不需額外努力。
- **決策可查詢。** 架構選擇、tradeoff、考慮過的替代方案全在一份
  log 裡。六週後問「我當時為什麼這樣做」會得到真實答案。
- **Agent 提案就只是提案。** Agent 想新增的高承諾物件 — 新目標、
  新專案、新概念卡 — 進入待確認佇列。你確認或拒絕。主控權留在你
  身上。
- **跨 session 連續性。**「下次繼續做 Y」是下次 session 第一眼看到
  的結構化筆記，不用再講一次。
- **抗健忘訊號。** Server 追蹤工具呼叫模式，把忘記的事浮上檯面 —
  卡住的 in-progress 任務、累積的 pending 提案、登錄了決策卻沒做
  session 開頭 recall。AI 沒辦法強迫自己自律，把缺口暴露出來是次
  好的辦法。

## 怎麼組織

七個 bounded context。每個擁有一片模型跟一套窄定義的詞彙；混在一起
就會壞掉。

| Context | 擁有什麼 |
|---|---|
| **GTD** | 目標 → 專案 → 任務（含重要性與討論脈絡），加 activity log。 |
| **Decisions** | 架構與設計決策，含理由與替代方案。 |
| **Knowledge** | 文章、TIL、書籤、Zettelkasten 筆記 — 全文與語意搜尋，ingest 時去重。 |
| **Learning** | 間隔重複概念卡，跑 FSRS 排程。系統可從存進來的知識自動提案概念卡。 |
| **Sessions** | 跨 session 的交接筆記 — 「下次要繼續什麼」。 |
| **Proposals** | Agent 原創、等使用者確認的物件。 |
| **Workspace** | 追蹤的 Git repo，含狀態、已知問題、下一步計畫。 |

每個 entity 帶可選的 workspace scope，多個獨立的個人資料庫可以共用
同一個 instance。

## 設計哲學

**結構優先於 prompt。** 記憶檔案跟超大 context window 是「AI 認識
你」的傳統路。反過來的路其實更誠實：把你想要 AI 記得的部分編碼成
明確的 schema，每個 agent 讀的都是同一個模型。Agent 之間沒漂移、
沒有「我記得你提過…」，就是資料。

**使用者保留決定權。** Agent 提案，你確認。高承諾物件走待確認佇
列，而不是直接建立。摩擦本身就是重點 — 一個替你做決定的系統最後
會讓你不會做決定。

**讓遺忘可見。** 再自律的 agent 都會忘記收尾。與其期待它記得，
server 把每次工具呼叫都記下來，並暴露一份讀數，把模式講出來 —
*加了好幾個任務沒一個完成*、*pending 提案累積*。一個小 hook 把同
一份讀數寫到磁碟，下次 session 還沒講話就會看到上次留下的尾巴。

**Workflow 工具，不是原始 CRUD。** Agent 接觸面提供「拿今天的
context」、「確認一個計畫」、「登錄一個決策」、「填一份 session
交接」這種動詞操作。Schema 藏在動詞後面；規則住在工具層，而不是
散落在每個 client 的 prompt 裡。

## 範圍與界限

- **設計上單租戶。** 一個人，多個 agent。沒有團隊模式、沒有 RBAC。
  你 fork 來自己 self-host，workspace scope 會幫你隔離；想邀協作
  者一起用，這套不適合。
- **個人步調。** 一個人開發跟維運。release 不規律，會有 breaking
  change，儀表板有些地方還沒上樣式。

## 怎麼跑起來

Self-host 步驟、環境變數、貢獻者流程在 [docs/installation.md] 與
[CONTRIBUTING.md]。

## 授權

[MIT](./LICENSE).

[docs/installation.md]: ./docs/installation.md
[CONTRIBUTING.md]: ./CONTRIBUTING.md
