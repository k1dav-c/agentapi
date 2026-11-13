# 安全性

[English](security.md) | 繁體中文

AgentAPI 沒有使用者或角色的概念：連得到 API 的人，就能做 agent 能做的任何事。預設不需要
token，在所有網路介面上監聽，只要 `Host` 標頭指向本機（`localhost`、`127.0.0.1`、
`[::1]`）就接受連線。主機檢查能防止網頁透過 DNS rebinding 呼叫它，但擋不住網路上的程式，
因為它們可以送出任何 `Host` 標頭。在其他人連得到這個連接埠之前，請先把伺服器保護好。
沒有設定 token、又監聽其他主機連得到的位址時，AgentAPI 會在啟動時記錄一則警告。

## API token 驗證

所有 API 端點都可以用 Bearer token 保護。為了向下相容，驗證功能**預設為停用**。

```bash
# 自動產生隨機 token（啟動時輸出至標準錯誤）
agentapi server --api-token -- claude

# 使用指定的 token
agentapi server --api-token=my-secret -- claude

# 透過環境變數設定
AGENTAPI_API_TOKEN=my-secret agentapi server -- claude
```

自動產生的 token 在原地重新啟動（SIGUSR2、`POST /update`、`agentapi restart`）後仍然相同，
用戶端不必重新登入；重新執行 `agentapi server` 才會產生新的 token。

啟用後，每個 API 請求都必須帶上 `Authorization: Bearer <token>` 或聊天介面的
session cookie。靜態檔案路由（`/`、`/chat/*`）不受限制，讓瀏覽器不需要 token 也能
開啟聊天介面。token 以固定時間（constant time）比對。

瀏覽器無法替事件串流或圖片加上 `Authorization` 標頭，所以聊天介面會用 token 換一個
cookie 來登入：

- 開啟 `http://localhost:3284/chat/embed#token=<token>`。自動產生 token 時，伺服器
  啟動時會印出這個連結。token 放在 URL 的 fragment 中，瀏覽器不會把它送到伺服器、
  proxy 或存取紀錄；頁面換到 cookie 後就會把它從網址列移除。連結指向沒有設定
  `--api-token` 的伺服器時，會直接開啟聊天介面。
- 或直接開啟聊天介面：伺服器回應 401 時，介面會請你輸入 token。對話框會顯示是哪一台
  伺服器在要求、在它不是頁面本身的 origin 時提出警告，瀏覽器沒有保留 session cookie 時
  也會說明。按下 Cancel 之後，**Reconnect now** 會再詢問一次。

帶著 `Authorization: Bearer <token>` 呼叫 `POST /auth/session` 會設定 cookie
（`agentapi_session_<hash>`，HttpOnly、SameSite=Strict、`Path=/`，透過 HTTPS 或經由送出
`X-Forwarded-Proto: https` 的 proxy 連線時加上 Secure）。cookie 裡存的是 token 的
HMAC 而不是 token 本身，所以更換 token 會讓所有瀏覽器登出。cookie 名稱也由 token 而來，所以
同一台主機上使用不同 token 的伺服器，各自維持獨立的登入狀態。`DELETE /auth/session` 登出，
`GET /auth/session`（不需要憑證）回報 `auth_required` 與 `authenticated`。cookie 是
SameSite=Strict，其他網站的頁面無法帶著它發出請求。從其他 origin 提供的聊天介面（例如在
3000 連接埠執行的 `bun run dev`）需要把該 origin 加進 `--allowed-origins`；
`--allowed-origins '*'` 不允許帶 cookie 的請求。不同的連接埠不算不同的網站，而預設允許的來源
包含給聊天介面開發用的 `http://localhost:3000` 和 `http://localhost:3001`：在共用的主機上，
請把 `--allowed-origins` 設成你實際使用的來源，讓已登入瀏覽器的 cookie 只對它們有效。

## 監聽位址

伺服器預設監聽所有網路介面。用 `--bind`（或 `AGENTAPI_BIND`）可以只監聽一個位址，
例如只監聽 loopback，讓網路上的其他主機無法連線。它接受不含連接埠的 IP 位址或主機
名稱；連接埠仍由 `--port` 指定。

```bash
agentapi server --bind 127.0.0.1 -- claude
agentapi server --bind ::1 -- claude          # IPv6 本機回送位址
AGENTAPI_BIND=127.0.0.1 agentapi server -- claude
```

`--bind` 決定誰能連線；下方的 `--allowed-hosts` 決定連得上的請求可以使用哪些
`Host` 標頭。

## 允許的主機

伺服器預設只接受 `Host` 標頭為 `localhost`、`127.0.0.1` 或 `[::1]` 的請求。要在其他地方架設 AgentAPI，請設定 `AGENTAPI_ALLOWED_HOSTS` 環境變數或 `--allowed-hosts` 旗標。主機只能填主機名稱（不含連接埠）；伺服器授權時會忽略請求中的連接埠。這只檢查 `Host` 標頭；若要讓伺服器完全不對網路開放，請再用 `--bind 127.0.0.1` 只監聽 loopback（見[監聽位址](#監聽位址)）。

要允許來自任何主機的請求，請用 `*` 作為允許的主機。

```bash
agentapi server --allowed-hosts '*' -- claude
```

要允許特定主機，請使用：

```bash
agentapi server --allowed-hosts 'example.com' -- claude
```

指定多個主機時，`--allowed-hosts` 旗標以逗號分隔，`AGENTAPI_ALLOWED_HOSTS` 環境變數以空白分隔。

```bash
agentapi server --allowed-hosts 'example.com,example.org' -- claude
# 或
AGENTAPI_ALLOWED_HOSTS='example.com example.org' agentapi server -- claude
```

## 允許的來源

伺服器預設允許來自 `http://localhost:3284`、`http://localhost:3000` 和 `http://localhost:3001` 的 CORS 請求。要更改哪些來源可以發出跨來源請求，請設定 `AGENTAPI_ALLOWED_ORIGINS` 環境變數或 `--allowed-origins` 旗標。

要允許來自任何來源的請求，請用 `*` 作為允許的來源：

```bash
agentapi server --allowed-origins '*' -- claude
```

要允許特定來源，請使用：

```bash
agentapi server --allowed-origins 'https://example.com' -- claude
```

指定多個來源時，`--allowed-origins` 旗標以逗號分隔，`AGENTAPI_ALLOWED_ORIGINS` 環境變數以空白分隔。來源必須包含通訊協定（`http://` 或 `https://`），也可以使用萬用字元（例如 `https://*.example.com`）：

```bash
agentapi server --allowed-origins 'https://example.com,http://localhost:3000' -- claude
# 或
AGENTAPI_ALLOWED_ORIGINS='https://example.com http://localhost:3000' agentapi server -- claude
```

其他來源的網頁不能讓伺服器做任何改動（GET、HEAD、OPTIONS 以外的方法），除非它的來源列在清單上。
瀏覽器送這類請求前不會先詢問（表單、`no-cors` 模式的 `fetch`）；CORS 只擋住網頁讀取回應，所以沒有
`--api-token` 時，你開過的任何網站都能替 agent 送任務，或在終端機裡打字。`*` 讓每個網頁都能讀取
回應，但不信任任何網頁送出這類請求：需要的來源要明確列出。伺服器依瀏覽器加上的 `Sec-Fetch-Site`
和 `Origin` 標頭判斷來源，所以瀏覽器以外的用戶端（curl、SDK、hub）不受影響。

## API 用戶端能做什麼

- 讓 agent 執行它能執行的任何事。除非命令列指定了其他模式，或 Claude Code 在沙箱外以 root
  執行（此時以 `default` 啟動），agent 預設以 bypass 權限模式啟動，工具直接執行、不詢問。
  Claude Code 會加上 `--allow-dangerously-skip-permissions`（在沙箱外以 root 執行時除外），
  所以用戶端可以把任何工作階段切換到 bypass。除非命令列指定了政策，Codex 的預設值會覆蓋
  `~/.codex/config.toml` 裡的政策。
- 在[終端機](agents.zh-TW.md#終端機)裡執行指令（預設開啟；`--terminal=false` 可以關閉）。
- 透過檔案檢視器的端點讀取 agent 工作目錄中的檔案；工作目錄以外的路徑（包括透過 symlink）
  一律拒絕，diff 也只會讀取 git 儲存庫。HTML 預覽的腳本在沙箱中執行，無法存取聊天介面或
  API。`GET /files/find` 會列出工作目錄底下路徑結尾相符的檔案路徑。
- 從 agent 自己的紀錄（`~/.claude`、`~/.codex`、`~/.pi`）讀取它在工作目錄中先前工作階段的對話、
  sub agent 的步驟和花費，並開啟其中任何一個工作階段。
- 上傳最大 100 MB 的檔案；只有圖片會回傳。
- 修改 webhook，以及重新啟動 agent。webhook 可以把對話（`message.created`、
  `tool.completed`）送到任何網址；`GET /webhook` 不會回傳它的 secret。
- 開啟 `--allow-update` 時，把 AgentAPI [更新](api.zh-TW.md#透過-api-更新)到最新正式發行版並重新啟動。用戶端
  無法指定版本或來源。

只要有其他人連得到伺服器，就請使用 `--api-token`，並搭配 `--bind` 或反向代理。
