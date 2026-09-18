# wayneblacktea 維運

線上服務怎麼查、production DB 怎麼連。**按需閱讀** —— 要查線上跑什麼、部署失敗了、或要直連 DB 時。

## Railway production

部署在 **Railway**(Project `wayneblacktea` / Environment `production` / Service `wayneblacktea`),由 master 觸發。CLI:`railway status` / `railway logs` / `railway variables` / `railway deployment list`。

**問「線上是什麼狀態」一律走 `railway` CLI,NEVER 用 `lsof -i :PORT` / `curl localhost` 回答。**
MCP 設定裡寫的是 `http://127.0.0.1:<port>/mcp`,很容易看到就一路追下去 —— 但那是「這台機器怎麼連」,不是「服務跑在哪」。本機沒東西在聽不代表線上沒跑,反之亦然。

**auto-deploy 斷掉時的重連是 maintainer-only 動作** —— Railway 的 GitHub OAuth 重新授權要在瀏覽器裡做,代不了。所以「merge 完自動部署」不成立時,正確反應是回報並請 maintainer 重連,不是反覆 `railway up` 硬推;在重連之前,每次 merge 都要手動 `railway up`。

### 部署失敗時先查什麼

**Discord bot 起不來已經不會再讓整台 server 陪葬**(PR #185)。`startDiscordBotIfConfigured`(`cmd/server/main.go:645`)現在有三條不致命的路:`DISCORD_ENV=local`(`:646-649`)、沒有 token(`:653-654`)、以及 `Start()` 失敗轉 `degraded`。只有 `New()` 失敗仍然致命 —— 那是 env 設錯(例如有 token 卻空白名單),修得掉,應該吵。

代價是**那個故障現在是安靜的**:以前 Discord 掛會讓部署 FAILED,現在只會變成一個欄位。那個欄位是唯一的替代訊號:

```bash
curl -s -H "X-API-Key: $API_KEY" https://wayneblacktea-production.up.railway.app/api/health/discord
```

路由註冊在 `cmd/server/main.go:304`,四態 `starting` / `ok` / `degraded` / `unconfigured`,**一律回 200** —— 所以不能用 HTTP 狀態碼判,要讀 body 的 `status` 欄位。它在 `api` group 內,不帶 `X-API-Key` 回 401。

⚠ **event handler goroutine 的 panic 仍未涵蓋**。discordgo 每個回呼跑在自己的 goroutine(`event.go` 的 `go eh.eventHandler.Handle(...)`),而該 library 全樹 `recover()` 零命中;`echolog.Recover()` 是 echo middleware,接不到那些 goroutine。那條路還是會殺掉整台 server。

## Aiven PostgreSQL — 本機連線

`.env.local` 有兩個必要 env:`DATABASE_URL`(含 `?sslmode=require`)與 `PGSSLROOTCERT`(指向 repo 根的 `ca.pem`)。**MUST 同時帶 `PGSSLROOTCERT`**,否則 SSL certificate verify failed:

```bash
PGSSLROOTCERT=$(grep '^PGSSLROOTCERT=' .env.local | cut -d= -f2-) \
  psql "$(grep '^DATABASE_URL=' .env.local | cut -d= -f2-)" -c "SELECT 1"
```

**不可用**(三種都實測過):

- `PGSSLROOTCERT=""` —— 空值仍驗 cert
- `sslmode=no-verify` —— 這台機器的 psql 版本不認識這個值
- 不帶 `PGSSLROOTCERT` 直接 `psql "$DATABASE_URL"` —— certificate verify failed

**本機沒裝 psql 時**(`psql not found`)走 Docker client,`ca.pem` mount 進去、連線字串走 env 不外露:

```bash
DB=$(grep '^DATABASE_URL=' .env.local | cut -d= -f2-)
docker run --rm -v "$PWD/ca.pem:/ca.pem:ro" -e PGSSLROOTCERT=/ca.pem -e PGCONN="$DB" -e SQL="<your SQL>" \
  postgres:16-alpine sh -c 'psql "$PGCONN" -P pager=off -c "$SQL"'
```

這個 DB = dashboard 與 MCP 寫入的**同一個 production DB**(驗法:`SELECT` 某筆剛用 MCP 改過的 task,status 一致即同庫)。MCP 的 `list_tasks` 預設只回未結單,已結的(completed / cancelled)只能這樣直查或直改。

⚠ **這條路在某些網路上完全不通** —— Aiven 的連接埠會跟 SSH port 22 一起被擋掉。先分流再決定走不走:

```bash
nc -z -G 15 <aiven-host> <port>; echo "RC=$?"      # RC=1 → 被擋,psql 這條路放棄
```

被擋時的替代路徑有兩條:production 自己的 HTTP API(`GET /api/tasks?status=all`,帶 `X-API-Key`),或 Aiven 主控台的 query editor。
