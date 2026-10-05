# AgentAPI

[English](README.md) | 繁體中文

透過 HTTP API 控制 [Claude Code](https://github.com/anthropics/claude-code)、[AmazonQ](https://aws.amazon.com/developer/learning/q-developer-cli/)、[Opencode](https://opencode.ai/)、[Goose](https://github.com/block/goose)、[Aider](https://github.com/Aider-AI/aider)、[Gemini](https://github.com/google-gemini/gemini-cli)、[GitHub Copilot](https://github.com/github/copilot-cli)、[Sourcegraph Amp](https://ampcode.com/)、[Codex](https://github.com/openai/codex)、[Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started)、[Pi](https://pi.dev/)、[Auggie](https://docs.augmentcode.com/cli/overview) 和 [Cursor CLI](https://cursor.com/en/cli)。

![agentapi-chat](https://github.com/user-attachments/assets/57032c9f-4146-4b66-b219-09e38ab7690d)

AgentAPI 可以用來：

- 為各種 coding agent 打造統一的聊天介面
- 作為 MCP 伺服器的後端，讓一個 agent 控制另一個 coding agent
- 建立把 pull request 審查交給 agent 處理的工具
- 還有更多用途！

## 此 fork 的變更

這個 fork 以上游 v0.12.2 的程式碼為基礎。它保留了原本的終端機模擬與 HTTP API
模型，同時把 AgentAPI 擴充成一個更完整的工作環境，方便操作長時間執行的 coding agent。

### 工作階段工作區與聊天介面

內建的聊天介面以任務為單位來組織，而不是一整串平鋪的對話紀錄。每一則使用者請求都會成為
一個可以瀏覽的任務，並附上對應的回應、思考區塊、工具呼叫和背景活動。

任務以對話紀錄的形式呈現：左側有時間軸，你的提示詞放在標示「You」的色塊裡，agent
的回合則以它的標誌和名稱開頭（例如 Pi），回覆以 Markdown 顯示，工具呼叫則是一行一列（`$ command`、結束代碼、耗時、輸出預覽），在寬螢幕上思考
內容會以旁註顯示。輸入框上方的狀態列會顯示 agent 目前正在做什麼；當 agent 在等你回答時，
標頭、分頁標題和佇列都會顯示「Needs you」。較長的任務會先顯示最近的 40 個步驟。

工作階段瀏覽器（Session Explorer）提供：

- 目前對話中出現的連結與檔案路徑；
- 可直接跳到先前任務的索引；
- 單一任務的 Markdown 預覽與匯出；
- 當解析出來的對話不夠用時，可以直接存取即時終端機；
- 不必離開聊天介面就能設定 MCP 伺服器和 Temporal/Discord；
- 提供兩步驟重新啟動（Restart）的 Session 分頁（用 ⌘K / Ctrl+K 開啟 Explorer）。

每則訊息都有自己的 markdown/raw 切換開關，可以個別切換顯示模式，不會影響對話的其他部分。

可以直接在輸入框貼上圖片：貼上的截圖會像附件一樣上傳，貼上文字則照常貼上。附件中的圖片和已送出訊息裡的圖片會顯示成縮圖，點開可看原圖；agent 收到的是檔案路徑。

介面也改善了行動裝置版面、附件處理、可搜尋的工具活動、連線狀態指示，以及更精簡的
工具呼叫卡片。標頭會顯示 agent 的標誌（Claude 的橘色方塊、Pi 的 logo）和 AgentAPI 版本。

**TTY mode**（TTY 模式）是聊天畫面顯示不正常時的備用方案：按下標頭的終端機按鈕，
就會把對話換成 agent 的終端機畫面（xterm.js），並把每個按鍵直接送給 agent，包含方向鍵、
Ctrl/Alt 組合鍵、貼上的內容和輸入法輸入。在手機上會有一排按鍵列，提供 Esc、Tab、方向鍵、
Enter 和 Ctrl 鍵。字型大小會隨視窗縮放（11–20px）；鏡像畫面使用的是 agent 實際的終端機
寬度，所以如果想要每行更長，請用 `--term-width 120` 啟動 AgentAPI。

**背景提醒**：分頁在背景時，當 agent 需要你處理，圖示會出現琥珀色圓點；agent 工作中時
是藍色圓點；任務完成時則是綠色圓點。瀏覽器通知可以從狀態選單開啟。

### 訊息佇列與連線復原

在 agent 忙碌時送出的訊息會放進 FIFO 佇列，而不是直接被拒絕。排隊中的訊息在送出前，
可以透過 API 或聊天介面查看、編輯或刪除。

長時間開著的瀏覽器工作階段有多重保護：SSE heartbeat、自動重新連線、失效連線偵測，
以及在本機復原那些還沒送達伺服器就失敗的訊息。網頁標題和工作階段標頭會顯示目前的任務、
agent 狀態和連線狀態。

重新開啟很長的對話也很快：聊天介面會把對話紀錄快取在瀏覽器中（IndexedDB），先立即顯示，
再向伺服器只要求之後有變動的部分。

相關 API 包括：

- `GET /queue` 和 `PUT/DELETE /queue/{id}`：管理佇列；
- `GET /events`：即時訊息、狀態、錯誤、結構化活動（rich activity）和 heartbeat 事件。
  加上 `?sync=1` 時，串流會以 `session_sync` 事件開頭，每次更新都會帶有 `seq`；
  用 `&since=<seq>&epoch=<epoch>` 重新連線時，只會重播有變動的部分；
- `GET /title`：取得目前工作階段的可讀標題。

### Claude、Codex 與 Pi 的結構化活動

除了解析終端機快照之外，這個 fork 也能監看 Claude、Codex 和 Pi 的工作階段紀錄檔。
這樣就能取得結構化的思考區塊、工具呼叫、工具結果、用量資訊，以及穩定的訊息識別碼，
這些資訊不一定能單靠終端機輸出可靠地還原。

- `GET /rich-messages` 回傳結構化的對話。
- `GET /timeline` 回傳正規化後的工作階段事件，適合用來匯出、稽核或打造其他介面。
- 背景任務和委派出去的任務會持續顯示，並標示執行中、已完成或失敗的狀態與輸出細節。
- Claude 的思考區塊會以可收合的區段，直接顯示在任務時間軸中。
- Codex 啟動時會加上 `-c model_reasoning_summary="auto"`（除非你自己設定了），
  讓它的紀錄檔包含可閱讀的推理摘要，而不是只有加密過的推理內容。

對於其他 agent，或是拿不到工作階段紀錄檔的環境，仍然會退回使用終端機解析。

### MCP 管理

AgentAPI 執行期間可以管理 Claude、Codex 和 Pi 的 MCP 伺服器。實作上會保留
`.mcp.json`（Claude）、`$CODEX_HOME/config.toml`（Codex）或 `~/.pi/agent/mcp.json`（Pi）
中不相關的設定。

API 和 Session Explorer 支援：

- 讀取或整個取代 MCP 伺服器對照表；
- 新增、更新和刪除個別伺服器；
- 檢查遠端 HTTP 連線狀況，並解析本機 stdio 執行檔；
- 儲存、匯入、匯出和套用可重複使用的 MCP 設定檔（profile）；
- 可選擇重新啟動 PTY agent，讓變更立即生效。

重新啟動 agent 時，AgentAPI 本身和它的 HTTP/SSE 用戶端都會保持連線。子 agent 會取得
新的行程和工作階段紀錄監看器，但先前記憶體中的對話脈絡不會保留。重新啟動前，AgentAPI
會先更新 Codex（`codex update`）和 Pi（`pi update --self`），讓重新啟動後的 agent 是
最新版本；Claude Code 則會自行更新。

### 執行狀態 webhook

每當執行狀態在 `running` 和 `stable` 之間切換時，AgentAPI 可以送出 HTTP POST。
Webhook 可以用 CLI 旗標或 `AGENTAPI_WEBHOOK_*` 環境變數初始化，之後再透過
`GET/PUT /webhook` 或 Session Explorer 查看或修改。

傳送是非同步進行的，逾時時間和重試次數都可以設定。你也可以選擇提供 Go `text/template`
payload 範本，依照接收端的需求調整 POST 內容 —— 可用的欄位有 `.ID`、`.Type`、`.CreatedAt`、
`.RunID`、`.Status`、`.PreviousStatus`、`.AgentType` 和 `.Transport`。沒有設定範本時，
會原封不動地送出預設的 JSON payload。

```bash
agentapi server \
  --webhook-url https://example.com/agentapi/events \
  -- claude
```

### 透過 Temporal 從 Discord 回覆

選用的 `agentapi discord` 服務讓你在關掉網頁介面後，還能從 Discord 繼續同一個 agent
工作階段。它會為待處理的回應啟動一個 Temporal workflow、送出機器人通知，並在允許的
Discord 使用者回覆該通知時，等待 `human_response` signal。接著由 worker Activity 把回覆
送進 AgentAPI。過程中網頁介面仍然可以正常使用。

這支援一般的對話追問，以及可辨識的 PTY 編號確認對話框。通知和瀏覽器是否開著無關。
AgentAPI 和橋接服務都必須持續執行；Temporal 保存的是 workflow 狀態，而不是 agent 的
終端機行程。請參閱[安裝、設定與限制](docs/discord-temporal.md)。

### 自我更新

AgentAPI 可以從命令列自我更新：

```bash
agentapi update          # download and install the latest release
agentapi update --check  # check without downloading
agentapi update --force  # skip version comparison
```

在取代正在執行的執行檔之前，會先用該版本的 `checksums.txt` SHA-256 清單驗證發行版的
二進位檔。如果清單不存在、格式錯誤，或和下載的檔案不符，就會拒絕更新。

### API token 驗證

所有 API 端點都可以用 Bearer token 保護。為了向下相容，驗證功能**預設為停用**。

```bash
# Auto-generate a random token (printed to stderr on startup)
agentapi server --api-token -- claude

# Use a specific token
agentapi server --api-token=my-secret -- claude

# Via environment variable
AGENTAPI_API_TOKEN=my-secret agentapi server -- claude
```

啟用後，每個 API 請求都必須帶上 `Authorization: Bearer <token>`。靜態檔案路由
（`/`、`/chat/*`）不受限制，讓瀏覽器不需要 token 也能開啟聊天介面。

### 互動式提示支援

聊天介面會偵測互動式 TUI 提示 —— 例如 Claude Code 的計畫核准對話框或權限確認、Codex
的核准請求，以及 Pi 的專案信任提示 —— 並把它們以決策卡片（decision card）的形式固定在
輸入框上方。按數字鍵即可選擇選項。多選題（Claude Code 帶有 `multiSelect` 的
AskUserQuestion）會顯示核取方塊和一個 Continue 按鈕，按下後會前往下一題或檢查步驟。
agent 回答中的編號清單不會被誤判為選項。`GET /status` 會以 `terminal_prompt` 回報該提示。

### Kimi Code CLI

這個 fork 新增了 `kimi` agent 類型、`kimi` 執行檔的自動偵測、聊天介面標示、終端機訊息
格式化、就緒狀態偵測，以及其啟動狀態的測試。

Kimi 可以使用一般的互動式 PTY 傳輸方式：

```bash
agentapi server -- kimi
```

完成一次 `/login` 之後，也可以使用 Kimi 原生的 ACP 伺服器：

```bash
agentapi server --type=kimi --experimental-acp -- kimi acp
```

### Pi coding agent

`pi` agent 類型會在互動式終端機介面中執行 [Pi coding agent](https://pi.dev/)
（`npm install -g @earendil-works/pi-coding-agent`）。當執行檔名稱為 `pi` 時會自動偵測：

```bash
agentapi server -- pi
```

在 Pi 中用 `/login` 登入一次即可（可以用聊天介面的 TTY mode），或是設定供應商金鑰，
例如 `ANTHROPIC_API_KEY`。AgentAPI 會追蹤 Pi 的工作階段紀錄檔（`~/.pi/agent/sessions/`，
或 `$PI_CODING_AGENT_DIR` / `$PI_CODING_AGENT_SESSION_DIR`）來取得結構化訊息，因此聊天
介面會像處理 Claude Code 和 Codex 一樣，顯示 Pi 的工具呼叫、思考內容和 token 用量。

Pi 的對話框（例如專案信任提示）可以直接在聊天介面中回答，標頭和分頁圖示也會顯示 Pi 的
logo。Explorer 的 MCP 分頁會管理 `~/.pi/agent/mcp.json` 中 Pi 使用者層級的伺服器，
重新啟動 agent 時也會先執行 `pi update --self`。

AgentAPI 啟動 Pi 時會指定自己的 `--session-id`（每次啟動或重新啟動都會換一個新的），
這樣即使有好幾個 Pi 行程共用同一個目錄，也能追蹤到正確的工作階段紀錄檔。如果你自己傳入
`--session`、`--continue`、`--resume` 或 `--session-id`，AgentAPI 就會把工作階段交給你
處理；使用 `--continue`/`--resume` 時，聊天介面會退回使用終端機輸出。

### 執行穩定性

這個 fork 也修正了許多問題，包括：寬字元的終端機游標追蹤、PTY 生命週期洩漏、並行事件
傳送、訊息追蹤的競爭條件、ACP 關閉流程、TUI 重新繪製造成的殘影、JSONL 監看器的 flush、
Claude delta 串流期間結構化訊息內容區塊的合併，以及 Claude Code 把工作階段轉存到新的 JSONL
時，執行期的工作階段檔案切換。這些變更的目的是讓 AgentAPI 在長時間工作階段、行程替換，
以及瀏覽器或網路暫時中斷時，都能保持穩定。

完整的 commit 歷史請參閱[與上游的完整比較](https://github.com/coder/agentapi/compare/main...k1dav-c:agentapi:main)。

## 快速開始

1. 安裝 `agentapi`：

   ```bash
   OS=$(uname -s | tr "[:upper:]" "[:lower:]");
   ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/");
   curl -fsSL "https://github.com/k1dav-c/agentapi/releases/latest/download/agentapi-${OS}-${ARCH}" -o agentapi && chmod +x agentapi
   ```

   你也可以從[發行頁面](https://github.com/k1dav-c/agentapi/releases)下載這個 fork
   最新的二進位檔。上游 `coder/agentapi` 的發行版二進位檔不包含 **此 fork 的變更**
   一節中說明的功能。

1. 確認安裝成功：

   ```bash
   agentapi --help
   ```

   > 在 macOS 上，如果系統提示無法驗證此二進位檔，請前往 `System Settings -> Privacy & Security`，點選「Open Anyway」（強制打開），然後再執行一次指令。

1. 執行 Claude Code 伺服器（假設系統已安裝 `claude`，且位於 `PATH` 中）：

   ```bash
   agentapi server -- claude
   ```

   > 如果出現 `claude` 不在 `PATH` 中的錯誤，但你在 shell 裡可以執行它，請用 `which claude` 取得完整路徑，改用完整路徑執行。

1. 傳送訊息給 agent：

   ```bash
   curl -X POST localhost:3284/message \
     -H "Content-Type: application/json" \
     -d '{"content": "Hello, agent!", "type": "user"}'
   ```

1. 取得對話紀錄：

   ```bash
   curl localhost:3284/messages
   ```

1. 開啟 http://localhost:3284/chat 試試聊天網頁介面。

## CLI 指令

### `agentapi server`

執行一個 HTTP 伺服器，讓你可以控制 agent。如果想用額外的參數啟動 agent，請把完整的 agent 指令放在 `--` 旗標之後。

```bash
agentapi server -- claude --allowedTools "Bash(git*) Edit Replace"
```

你也可以用 `agentapi` 執行 Aider 和 Goose agent：

```bash
agentapi server -- aider --model sonnet --api-key anthropic=sk-ant-apio3-XXX
agentapi server -- goose
```

Pi 透過它的互動式終端機介面執行（請參閱 [Pi coding agent](#pi-coding-agent)）：

```bash
agentapi server -- pi
```

Kimi Code 可以透過它的互動式終端機介面執行：

```bash
agentapi server -- kimi
```

Kimi Code 也提供原生的 ACP 伺服器。用 `kimi` 和 `/login` 登入一次之後，就可以使用
AgentAPI 的 ACP 傳輸方式：

```bash
agentapi server --type=kimi --experimental-acp -- kimi acp
```

> [!NOTE]
> 使用 Claude、Codex、Opencode、Copilot、Gemini、Amp 或 CursorCLI 時，請務必明確指定 agent 類型（例如：`agentapi server --type=codex -- codex`），否則訊息格式可能會出錯。當執行檔名稱為 `kimi` 或 `pi` 時，Kimi 和 Pi 會自動偵測；若使用包裝腳本或 ACP 模式，請加上 `--type=kimi` / `--type=pi`。

OpenAPI schema 可在 [openapi.json](openapi.json) 取得。

伺服器預設在 3284 埠執行。此外，伺服器也會在 http://localhost:3284/openapi.json 提供同一份 OpenAPI schema，並在 http://localhost:3284/docs 以文件介面列出可用的端點。

端點：

- GET `/messages` - 回傳與 agent 對話中的所有訊息清單
- POST `/message` - 傳送訊息給 agent。回傳 200 時，表示 AgentAPI 已偵測到 agent 開始處理這則訊息
- GET `/status` - 回傳向下相容的 `stable`/`running` 狀態、詳細的生命週期狀態
  （`starting`、`ready`、`running`、`restarting`、`exited` 或 `failed`）、工作階段 ID、
  單調遞增的執行 ID、AgentAPI 版本、agent 目前開啟的對話框（`terminal_prompt`）以及
  終端機寬度（`terminal_columns`）
- GET `/events` - agent 事件的 SSE 串流：訊息與狀態更新（加上 `?sync=1` 可在重新連線時增量重播）
- DELETE `/messages` - 清除所有對話狀態（訊息、結構化訊息、時間軸、錯誤），並重新啟動 agent 行程
- POST `/upload`：把檔案（最大 10 MB）存到伺服器的暫存上傳目錄並回傳路徑，訊息以 `@"<路徑>"` 引用
- GET `/uploads/{checksum}/{name}`：回傳上傳的圖片（依內容判斷 PNG、JPEG、GIF、WebP 或 BMP）供聊天介面顯示，其他檔案一律拒絕
- GET/PUT `/webhook` - 讀取或更新執行狀態 webhook 的傳送設定，不需要重新啟動 agent
- GET `/mcp` - 回傳 Claude、Codex 或 Pi 已設定的 MCP 伺服器，以及受管理的設定檔路徑
- PUT `/mcp` - 取代整組 MCP 伺服器；傳入 `?restart=true` 可重新啟動 PTY agent 並立即套用
- POST `/mcp/check` - 檢查遠端 HTTP 連線狀況，並解析 stdio 執行檔
- POST `/mcp/servers`、PATCH/DELETE `/mcp/servers/{name}` - 新增、更新或移除單一 MCP 伺服器
- GET `/mcp/profiles` - 匯出專案範圍的 MCP 設定檔（profile）
- PUT/DELETE `/mcp/profiles/{name}` - 匯入、取代或移除一個設定檔
- POST `/mcp/profiles/{name}/apply` - 用已儲存的設定檔取代目前使用中的 MCP 設定

#### API token 驗證

設定 `--api-token` 後，所有 API 請求都必須帶有 Bearer token（靜態聊天介面路由除外）：

```bash
agentapi server --api-token -- claude             # auto-generate and print to stderr
agentapi server --api-token=my-secret -- claude    # use a specific token
```

對應的環境變數是 `AGENTAPI_API_TOKEN`。設定之後，用戶端每次呼叫 API 都必須帶上
`Authorization: Bearer <token>`。沒有設定 `--api-token` 時，驗證功能為停用（向下相容）。

#### 允許的主機

伺服器預設只接受 host 標頭為 `localhost` 的請求。如果想在其他地方架設 AgentAPI，可以透過 `AGENTAPI_ALLOWED_HOSTS` 環境變數或 `--allowed-hosts` 旗標修改。主機只能填主機名稱（不含連接埠）；伺服器在授權時會忽略傳入請求中的連接埠部分。

要允許來自任何主機的請求，請用 `*` 作為允許的主機。

```bash
agentapi server --allowed-hosts '*' -- claude
```

要允許特定主機，請使用：

```bash
agentapi server --allowed-hosts 'example.com' -- claude
```

要指定多個主機時，使用 `--allowed-hosts` 旗標請以逗號分隔，使用 `AGENTAPI_ALLOWED_HOSTS` 環境變數則以空白分隔。

```bash
agentapi server --allowed-hosts 'example.com,example.org' -- claude
# or
AGENTAPI_ALLOWED_HOSTS='example.com example.org' agentapi server -- claude
```

#### 允許的來源

伺服器預設允許來自 `http://localhost:3284`、`http://localhost:3000` 和 `http://localhost:3001` 的 CORS 請求。如果想更改哪些來源可以對 AgentAPI 發出跨來源請求，可以透過 `AGENTAPI_ALLOWED_ORIGINS` 環境變數或 `--allowed-origins` 旗標修改。

要允許來自任何來源的請求，請用 `*` 作為允許的來源：

```bash
agentapi server --allowed-origins '*' -- claude
```

要允許特定來源，請使用：

```bash
agentapi server --allowed-origins 'https://example.com' -- claude
```

要指定多個來源時，使用 `--allowed-origins` 旗標請以逗號分隔，使用 `AGENTAPI_ALLOWED_ORIGINS` 環境變數則以空白分隔。來源必須包含通訊協定（`http://` 或 `https://`），並支援萬用字元（例如 `https://*.example.com`）：

```bash
agentapi server --allowed-origins 'https://example.com,http://localhost:3000' -- claude
# or
AGENTAPI_ALLOWED_ORIGINS='https://example.com http://localhost:3000' agentapi server -- claude
```

#### 執行狀態 webhook

設定 `--webhook-url` 後，每當執行狀態在 `running` 和 `stable` 之間切換時，就會送出 HTTP POST：

```bash
agentapi server \
  --webhook-url 'https://example.com/agentapi/events' \
  -- claude
```

對應的環境變數有 `AGENTAPI_WEBHOOK_URL`、`AGENTAPI_WEBHOOK_PAYLOAD_TEMPLATE`、
`AGENTAPI_WEBHOOK_TIMEOUT` 和 `AGENTAPI_WEBHOOK_MAX_ATTEMPTS`。逾時時間預設為 `10s`，
最多嘗試傳送 3 次。

AgentAPI 執行期間，仍然可以透過 `GET/PUT /webhook` 修改初始的 webhook 設定值。
Session Explorer 現在改用它的 Temporal 分頁來設定 Temporal 和 Discord 橋接。舊版的
webhook API 為了相容性仍然可以使用，但已不再是 Session Explorer 的整合方式。

請求內容的格式如下：

```json
{
  "id": "unique-delivery-id",
  "type": "run.status_changed",
  "created_at": "2026-07-26T12:00:00Z",
  "data": {
    "run_id": "agentapi-process-run-id",
    "status": "stable",
    "previous_status": "running",
    "agent_type": "claude",
    "transport": "pty"
  }
}
```

每個請求都會帶有 `X-AgentAPI-Delivery`、`X-AgentAPI-Event` 和 `X-AgentAPI-Timestamp` 標頭。

### `agentapi update`

把 agentapi 二進位檔更新到 GitHub 上的最新發行版。

```bash
agentapi update          # download and install the latest version
agentapi update --check  # only check if an update is available
agentapi update --force  # update even if already at the latest version
```

### `agentapi attach`

連接到執行中 agent 的終端機工作階段。

```bash
agentapi attach --url localhost:3284
```

按下 `ctrl+c` 即可中斷與工作階段的連接。

## 運作原理

AgentAPI 會執行一個記憶體內的終端機模擬器。它把 API 呼叫轉換成對應的終端機按鍵輸入，並把 agent 的輸出解析成一則一則的訊息。

### 把終端機輸出切分成訊息

訊息分成 2 種：

- 使用者訊息：由使用者傳給 agent
- Agent 訊息：由 agent 傳給使用者

為了從終端機輸出中解析出個別的訊息，我們採取以下步驟：

1. 在使用者送出任何訊息之前，初始的終端機輸出會被視為 agent 的第一則訊息。
2. 當使用者透過 API 送出訊息時，會在送出任何按鍵之前，先擷取一次終端機快照。
3. 接著把使用者訊息送給 agent。從這時開始，每當終端機輸出有變化，就會擷取新的快照，並和初始快照比對差異，出現在初始內容下方的新文字就會被視為 agent 的下一則訊息。
4. 如果在使用者送出新訊息之前，終端機輸出又有變化，就會更新這則 agent 訊息。

這樣就能把終端機輸出切分成一連串的訊息。

### 從 agent 訊息中移除 TUI 元素

每則 agent 訊息都包含一些對終端使用者沒有用處的多餘內容：

- 訊息開頭的使用者輸入。Coding agent 常常會把輸入內容回顯給使用者，讓它顯示在終端機上。
- 訊息結尾的輸入框。這是使用者平常輸入內容的地方。

AgentAPI 會自動移除這些內容。

- 對於使用者輸入，我們會移除包含使用者上一則訊息文字的那幾行。
- 對於輸入框，我們會在訊息結尾尋找包含常見 TUI 元素（例如 `>` 或 `------`）的行。

### 如果 Claude Code、Goose、Aider 或 Codex 更新了它們的 TUI，會怎麼樣？

把終端機輸出切分成一連串訊息的部分應該仍然可以運作，因為它不依賴 TUI 的結構。移除多餘內容的邏輯可能需要更新，以處理新的元素。AgentAPI 仍然可以使用，但 agent 訊息中可能會出現一些多餘的 TUI 元素。

## 開發藍圖

視使用者回饋而定，我們正在考慮以下功能：

- [支援 MCP 協定](https://github.com/coder/agentapi/issues/1)
- [支援 Agent2Agent 協定](https://github.com/coder/agentapi/issues/2)

## 長期願景

短期來看，AgentAPI 解決的是如何以程式控制 coding agent 的問題。隨著時間推移，我們希望看到主要的 agent 都推出正式的 SDK。有人可能會好奇，到那時候還需要 AgentAPI 嗎？我們認為這取決於 agent 廠商是決定統一採用共同的 API，還是各自堅持自家的專屬格式。

如果是前者，我們會停止維護 AgentAPI，改為推薦官方 SDK。如果是後者，我們的目標是讓 AgentAPI 成為能控制任何 coding agent 的通用轉接層，讓使用 AgentAPI 的開發者不必修改程式碼就能在不同 agent 之間切換。
