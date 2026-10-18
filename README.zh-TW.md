# AgentAPI

[English](README.md) | 繁體中文

透過 HTTP API 控制 [Claude Code](https://github.com/anthropics/claude-code)、[AmazonQ](https://aws.amazon.com/developer/learning/q-developer-cli/)、[Opencode](https://opencode.ai/)、[Goose](https://github.com/block/goose)、[Aider](https://github.com/Aider-AI/aider)、[Gemini](https://github.com/google-gemini/gemini-cli)、[GitHub Copilot](https://github.com/github/copilot-cli)、[Sourcegraph Amp](https://ampcode.com/)、[Codex](https://github.com/openai/codex)、[Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started)、[Pi](https://pi.dev/)、[Auggie](https://docs.augmentcode.com/cli/overview) 和 [Cursor CLI](https://cursor.com/en/cli)。

![agentapi-chat](https://github.com/user-attachments/assets/57032c9f-4146-4b66-b219-09e38ab7690d)

AgentAPI 可以用來：

- 為各種 coding agent 打造統一的聊天介面
- 作為 MCP 伺服器的後端，讓一個 agent 控制另一個 coding agent
- 建立把 pull request 審查交給 agent 處理的工具
- 還有更多用途！

## 關於這個 fork

這個 fork 以上游 v0.12.2 的程式碼為基礎。它保留了原本的終端機模擬與 HTTP API
模型作為預設，新增可自行啟用的 [native transport](#native-transport)，透過 Claude Code、
Codex 和 Pi 自己的結構化協定來驅動它們，同時把 AgentAPI 擴充成一個更完整的工作環境，
方便操作長時間執行的 coding agent。[CHANGELOG.md](CHANGELOG.md) 列出有哪些變更，
[docs/architecture.zh-TW.md](docs/architecture.zh-TW.md) 說明它的架構，完整的 commit 歷史
請參閱[與上游的完整比較](https://github.com/coder/agentapi/compare/main...k1dav-c:agentapi:main)。

## 目錄

- [功能總覽](#功能總覽)
- [快速開始](#快速開始)
- [Agent 與 transport](#agent-與-transport)
- [聊天介面](#聊天介面)
- [Native transport](#native-transport)
- [PTY transport](#pty-transport)
- [對話 API](#對話-api)
- [整合](#整合)
- [安全性](#安全性)
- [CLI 參考](#cli-參考)
- [HTTP API 參考](#http-api-參考)
- [架構](#架構)
- [開發](#開發)
- [執行穩定性](#執行穩定性)
- [開發藍圖](#開發藍圖)與[長期願景](#長期願景)

## 功能總覽

**透過 HTTP 驅動 coding agent**

- 用 [PTY transport](#pty-transport) 透過終端機介面控制 13 種 coding agent，把畫面轉成
  一則一則的訊息。
- 用 [native transport](#native-transport) 透過 Claude Code、Codex 和 Pi 自己的協定驅動
  它們；提供 ACP 伺服器的 agent 可以使用實驗性的 ACP transport（見 [Transport](#transport)）。
- 從 `GET /rich-messages` 和 `GET /timeline` 讀取結構化的活動：思考內容、工具呼叫與結果、
  token 用量。
- agent 工作中時送出的訊息會在可以編輯、重新排序的[佇列](#訊息佇列)中等待；使用 native
  transport 時，也可以把訊息 [steer 進執行中的回合或中斷它](#steerinterrupt-與佇列)。
- 透過 [server-sent events](#事件與增量同步) 即時追蹤一切，有 heartbeat，重新連線後只重播
  有變動的部分。

**Native agent**

- agent 提出的需求（工具核准、提問、計畫）會以[請求](#請求核准提問與計畫)送達，可以透過
  HTTP 或在聊天介面中回覆。
- 在 agent 執行中切換[權限模式、模型和推理強度](#工作階段設定)：bypass（預設）、auto、ask
  或 plan。
- 查看 [context 與用量限制](#context-與用量限制)用了多少，並[壓縮](#斜線指令與壓縮) context。
- 缺少或過期的[登入](#登入)會附上要執行的指令回報，登入之後 agent 會自己啟動。
- agent 工作目錄中共用的[終端機](#終端機)，方便登入和執行簡單的指令。
- [持久保存的工作階段](#持久保存的工作階段)：使用 `--state-db` 時，對話和 agent 自己的工作
  階段在 AgentAPI 重新啟動後都會保留。

**聊天介面**（`/chat`）

- [任務](#任務與對話紀錄)以對話紀錄的形式呈現，工具呼叫一行一列，思考內容以旁註顯示；
  [工作階段瀏覽器](#工作階段瀏覽器)提供連結、檔案、任務索引、匯出、MCP 伺服器和重新啟動。
- 終端機對話框和 native 請求的[決策卡片](#決策卡片)、agent 設定的選單、顯示 context 的圓環，
  以及斜線指令。
- 可以查看檔案、git diff，並預覽 HTML、Markdown 和圖片的[檔案檢視器](#檔案檢視器)、
  [TTY mode](#tty-mode) 和 native 終端機，在手機上也能使用。
- [背景提醒](#提醒連線復原與快取)、瀏覽器通知、對話紀錄快取，以及斷線後的復原。

**維運與整合**

- Claude Code、Codex 和 Pi 的 [MCP 管理](#mcp-管理)，可以使用可重複套用的設定檔；重新啟動
  agent 前會先更新 Codex 和 Pi。
- [執行狀態 webhook](#執行狀態-webhook) 和[人工回覆 API](#人工回覆-api)，讓機器人在 agent
  停下來時通知你，並把你的回覆傳回去。
- AgentAPI 的[自我更新](#agentapi-update)，會比對發行版的 checksum。
- [安全性](#安全性)：API token（聊天介面以 cookie 登入）、監聽位址，以及允許的主機與來源。

## 快速開始

1. 安裝 `agentapi`：

   ```bash
   OS=$(uname -s | tr "[:upper:]" "[:lower:]");
   ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/");
   curl -fsSL "https://github.com/k1dav-c/agentapi/releases/latest/download/agentapi-${OS}-${ARCH}" -o agentapi && chmod +x agentapi
   ```

   你也可以從[發行頁面](https://github.com/k1dav-c/agentapi/releases)下載這個 fork
   最新的二進位檔。上游 `coder/agentapi` 的發行版二進位檔不包含這個 fork 的功能。

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

   若要透過 Claude Code 自己的協定、而不是它的終端機介面來驅動它（核准會以結構化的請求
   送來、設定可以切換、聊天介面裡有終端機），請使用 [native transport](#native-transport)：

   ```bash
   agentapi server --transport native -- claude
   ```

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

## Agent 與 transport

### 支援的 agent

agent 類型決定 AgentAPI 怎麼整理 agent 的畫面，以及提供哪些功能。它來自 `--type`；沒有指定
時，若 agent 指令剛好是類型名稱之一，就用指令判斷（`agentapi server -- claude` 可以，
`-- /usr/local/bin/claude` 不行）；其他情況都以 `custom` 執行。

| Agent | 類型 | PTY | Native | 結構化活動 | MCP 管理 |
| --- | --- | :-: | :-: | :-: | :-: |
| [Claude Code](https://github.com/anthropics/claude-code) | `claude` | ✓ | ✓ | ✓ | ✓ |
| [Codex](https://github.com/openai/codex) | `codex` | ✓ | ✓ | ✓ | ✓ |
| [Pi](https://pi.dev/) | `pi` | ✓ | ✓ | ✓ | ✓ |
| [Kimi Code](https://www.kimi.com/zh-tw/help/kimi-code/cli-getting-started) | `kimi` | ✓ | | | |
| [Goose](https://github.com/block/goose) | `goose` | ✓ | | | |
| [Aider](https://github.com/Aider-AI/aider) | `aider` | ✓ | | | |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | `gemini` | ✓ | | | |
| [GitHub Copilot](https://github.com/github/copilot-cli) | `copilot` | ✓ | | | |
| [Sourcegraph Amp](https://ampcode.com/) | `amp` | ✓ | | | |
| [Auggie](https://docs.augmentcode.com/cli/overview) | `auggie` | ✓ | | | |
| [Cursor CLI](https://cursor.com/en/cli) | `cursor`（`cursor-agent`） | ✓ | | | |
| [Amazon Q](https://aws.amazon.com/developer/learning/q-developer-cli/) | `q`（`amazonq`） | ✓ | | | |
| [Opencode](https://opencode.ai/) | `opencode` | ✓ | | | |
| 其他終端機程式 | `custom` | ✓ | | | |

- **結構化活動**：思考內容、工具呼叫和 token 用量。使用 PTY transport 時從 agent 的工作階段
  紀錄檔讀取（見 [Claude、Codex 與 Pi 的結構化活動](#claudecodex-與-pi-的結構化活動)），
  使用 native transport 時直接來自它的協定。
- **MCP 管理**：Explorer 的 MCP 分頁和 `/mcp` 端點（見 [MCP 管理](#mcp-管理)）。
- 提供 ACP 伺服器的 agent（例如 Kimi Code 的 `kimi acp`）也可以使用實驗性的 ACP transport。

> [!NOTE]
> 使用 Claude、Codex、Opencode、Copilot、Gemini、Amp 或 CursorCLI 時，請務必明確指定 agent 類型（例如：`agentapi server --type=codex -- codex`），否則訊息格式可能會出錯。當執行檔名稱為 `kimi` 或 `pi` 時，Kimi 和 Pi 會自動偵測；若使用包裝腳本或 ACP 模式，請加上 `--type=kimi` / `--type=pi`。

### Transport

`--transport` 決定 AgentAPI 如何驅動 agent：

| | PTY（預設） | Native | ACP（實驗性） |
| --- | --- | --- | --- |
| 旗標 | `--transport pty` | `--transport native` | `--transport acp` 或 `--experimental-acp` |
| Agent | 上表所有 agent | Claude Code、Codex、Pi | 提供 ACP 伺服器的 agent |
| 方式 | 在終端機模擬器中執行 agent 的終端機介面，並讀取它的畫面 | 使用 agent 自己的協定：`claude -p` 的 stream-json、`codex app-server`、`pi --mode rpc` | 透過 agent 的 stdio 使用 Agent Client Protocol |
| 工具呼叫與思考 | 來自工作階段紀錄檔（Claude Code、Codex、Pi） | 從協定即時串流 | 回覆中的文字行 |
| 核准、提問、計畫 | 終端機對話框，從決策卡片或 TTY mode 回答 | 結構化的[請求](#請求核准提問與計畫) | 自動核准 |
| 對回合 steer 和 interrupt | | ✓ | |
| 設定、用量、壓縮 | | ✓ | |
| AgentAPI 重新啟動後保留 | `--state-file`：訊息 | `--state-db`：對話和 agent 的工作階段 | |
| 聊天介面中的終端機 | TTY mode：agent 自己的終端機 | 工作目錄中的 shell | |

```bash
agentapi server -- claude
agentapi server --transport native --state-db ~/.agentapi/myproject.db -- codex
agentapi server --type=kimi --experimental-acp -- kimi acp
```

`--state-db <檔案>`（僅限 native transport）會把對話保存在 SQLite 檔案裡，AgentAPI 用同一個
檔案再次啟動時會接續 agent 的工作階段；見[持久保存的工作階段](#持久保存的工作階段)。
`--terminal`（預設為 true，僅限 native transport）會提供 agent 工作目錄中的 shell；見
[終端機](#終端機)。`--state-file <檔案>`（僅限 PTY transport）會在關閉時保存從畫面解析出的
訊息，並在啟動時載入（設定它時，`--save-state` 和 `--load-state` 預設為 true）。

## 聊天介面

`/chat` 的聊天介面內建在執行檔裡，所有 transport 都能使用，桌面和手機都可以。它有適合手機的
版面、可搜尋的工具活動、連線狀態指示和精簡的工具呼叫卡片，標頭會顯示 agent 的標誌（Claude
的橘色方塊、Pi 的 logo）和 AgentAPI 版本。

### 任務與對話紀錄

內建的聊天介面以任務為單位來組織，而不是一整串平鋪的對話紀錄。每一則使用者請求都會成為
一個可以瀏覽的任務，並附上對應的回應、思考區塊、工具呼叫和背景活動。

任務以對話紀錄的形式呈現：左側有時間軸，你的提示詞放在標示「You」的色塊裡，agent
的回合則以它的標誌和名稱開頭（例如 Pi），回覆以 Markdown 顯示，工具呼叫則是一行一列（`$ command`、結束代碼、耗時、輸出預覽），在寬螢幕上思考
內容會以旁註顯示。輸入框上方的狀態列會顯示 agent 目前正在做什麼；當 agent 在等你回答時，
標頭、分頁標題和佇列都會顯示「Needs you」。較長的任務會先顯示最近的 40 個步驟。

每則訊息都有自己的 markdown/raw 切換開關，可以個別切換顯示模式，不會影響對話的其他部分。

### 工作階段瀏覽器

工作階段瀏覽器（Session Explorer）提供：

- 目前對話中出現的連結與檔案路徑；
- 可直接跳到先前任務的索引；
- 單一任務的 Markdown 預覽與匯出；
- 當解析出來的對話不夠用時，可以直接存取即時終端機；
- 不必離開聊天介面就能設定 MCP 伺服器；
- 提供兩步驟重新啟動（Restart）的 Session 分頁（用 ⌘K / Ctrl+K 開啟 Explorer）。

### 附件與圖片

可以直接在輸入框貼上圖片：貼上的截圖會像附件一樣上傳，貼上文字則照常貼上。附件中的圖片和已送出訊息裡的圖片會顯示成縮圖，點開可看原圖；agent 收到的是檔案路徑。

### 決策卡片

agent 在等待的東西會以決策卡片停靠在輸入框上方：使用 PTY transport 時是它的終端機對話框（見
[互動式提示支援](#互動式提示支援)），使用 native transport 時是它的[請求](#請求核准提問與計畫)，
等待登入時則是[登入卡片](#登入)。數字鍵可以選擇選項。在手機上卡片最多佔螢幕的 60%，被截成
兩行的標題可以展開來看完整內容。

### 檔案檢視器

聊天裡的檔案路徑可以點開 agent 正在處理的檔案。工具呼叫列上的檔名
（Claude Code 的 Read、Write、Edit、MultiEdit 和 NotebookEdit；Codex 的 apply_patch 和
view_image；Pi 的 read、write 和 edit）、agent 回覆中 inline code 裡的路徑（`lib/foo.go:12`
會開在第 12 行），以及 Explorer 的 Files 分頁，都會開啟有兩個分頁的檢視器：File 顯示行號和
語法高亮，Diff 顯示工具呼叫本身的變更，以及檔案相對於 HEAD 尚未 commit 的變更
（`GET /files/content`、`GET /files/diff`，超過 1 MiB 會截斷）。只能開啟 agent 工作目錄裡
的檔案。展開的工具呼叫會列出它碰到的檔案，並以 diff 顯示它的變更，原始輸入點一下就能看到。
在手機上檢視器是全螢幕。

HTML、Markdown 和 SVG 檔案還有第三個分頁 Preview，圖片檔則直接顯示圖片：

- HTML 頁面會在 iframe 中呈現，它以相對路徑引用的樣式表、腳本、圖片、字型和影音都會內嵌進頁面
  （`GET /files/preview`；每個檔案最多 4 MiB，合計 16 MiB；沒有內嵌的項目會列在提示中）。
  iframe 有 sandbox 且沒有自己的 origin，所以頁面的腳本可以執行，但碰不到聊天介面、它的登入
  狀態或 API；預覽開著的期間，localStorage、sessionStorage 和 cookie 會保存在記憶體中。
  會 import 其他檔案的腳本模組，以及頁面對相對網址發出的請求，都不會載入。
- Markdown 檔案會直接開在預覽：相對路徑的圖片會從工作目錄載入，相對連結會在檢視器中開啟該檔案
  （`#L12` 會開在那一行）。
- PNG、JPEG、GIF、WebP、BMP、ICO 和 SVG 圖片會以圖片顯示（`GET /files/image`）。

### TTY mode

TTY mode（TTY 模式）是聊天畫面顯示不正常時的備用方案：按下標頭的終端機按鈕，
就會把對話換成 agent 的終端機畫面（xterm.js），並把每個按鍵直接送給 agent，包含方向鍵、
Ctrl/Alt 組合鍵、貼上的內容和輸入法輸入。在手機上會有一排按鍵列，提供 Esc、Tab、方向鍵、
Enter 和 Ctrl 鍵。字型大小會隨視窗縮放（11–20px）；鏡像畫面使用的是 agent 實際的終端機
寬度，所以如果想要每行更長，請用 `--term-width 120` 啟動 AgentAPI。

### 使用 native transport 時

使用 `--transport native` 時，agent 提出的請求（工具核准、它的提問、
待核准的計畫）會以卡片停靠在輸入框上方（在手機上最多佔螢幕的 60%）；agent 工作中時，送出
按鈕也可以把訊息 steer 進正在進行的回合，或中斷它。輸入框的工具列可以切換 agent 的權限模式、
模型和推理強度，並顯示 context 用了多少和用量限制；輸入框可以使用 `/model`、`/compact` 等
斜線指令；終端機按鈕會開啟 agent 工作目錄中的 shell，取代需要 agent 自己終端機的 TTY mode
（畫面相關的檢視仍會隱藏，ACP 也是）。請參閱 [Native transport](#native-transport)。

### 提醒、連線復原與快取

分頁在背景時，當 agent 需要你處理，圖示會出現琥珀色圓點；agent 工作中時
是藍色圓點；任務完成時則是綠色圓點。瀏覽器通知可以從狀態選單開啟。

長時間開著的瀏覽器工作階段有多重保護：SSE heartbeat、自動重新連線、失效連線偵測，
以及在本機復原那些還沒送達伺服器就失敗的訊息。網頁標題和工作階段標頭會顯示目前的任務、
agent 狀態和連線狀態。

重新開啟很長的對話也很快：聊天介面會把對話紀錄快取在瀏覽器中（IndexedDB），先立即顯示，
再向伺服器只要求之後有變動的部分。

## Native transport

AgentAPI 預設會在終端機模擬器中執行 agent，並讀取它的畫面（PTY transport）。使用
`--transport native` 時，則改用 Claude Code、Codex 和 Pi 自己的結構化協定來驅動它們：
Claude Code 的 stream-json 輸入輸出加上它的 control protocol、`codex app-server`
（走 stdio 的 JSON-RPC）以及 `pi --mode rpc`。訊息、rich messages（工具呼叫、思考、
用量）和時間軸都直接來自協定，所以不必解析畫面，也不必追蹤工作階段紀錄檔；agent
提出的請求也會以結構化的 requests 送達，而不是終端機對話框。

```bash
agentapi server --transport native -- claude --model sonnet
agentapi server --transport native -- codex -a on-request
agentapi server --transport native -- pi --provider openai-codex --model gpt-5.6-luna --thinking low
```

- Native transport 需要自行啟用；預設仍是 PTY（`--transport pty`）。`--transport acp`
  等同 `--experimental-acp`，而 `--experimental-acp` 不能和 `--transport native` 一起使用。
- 使用哪個 adapter 由 agent 類型決定：`claude`、`codex` 或 `pi`，依執行檔名稱或
  `--type` 判斷。其他 agent 類型會在啟動時被拒絕。若想在沒有真正 agent 的情況下開發聊天
  介面，`--transport native --type custom -- native-demo` 會執行一個模擬的 agent（見
  [chat/README.md](chat/README.md)）。
- 文字和思考內容會即時增長：rich message 在串流時會整則取代（每則訊息每秒最多十次快照；
  串流暫停時，最新的狀態也會在 100 毫秒內送達）。對 Claude Code，AgentAPI 會要求摘要式的
  adaptive thinking；Codex 的每個 turn 都會要求詳細的推理摘要；Pi 的思考內容則在 Pi 寫出時
  就串流顯示。
- `GET /status` 會回報 `transport`（`native`）、agent 的 `capabilities`、
  `pending_requests`、它的 `settings` 和 `usage`，以及 `terminal`，`status_change` 事件也
  會帶有這些資訊。agent 只是在等某個請求的回覆時，`status` 為 `stable`，和終端機對話框的
  情況一樣。
- agent 在自己的 process group 中執行，所以它啟動的 MCP 伺服器和指令會跟著它一起停止。
  停止 agent 時（AgentAPI 結束或重新啟動），會先中斷進行中的回合，最多等 3 秒讓它結束，
  工作階段紀錄才不會停在沒有結果的工具呼叫；接著關閉它的輸入讓它自行結束，3 秒後送出
  SIGTERM，再過 2 秒送出 SIGKILL。
- 從另一個 agent 的工作階段中啟動時（例如由 Claude Code 的 Bash 工具啟動），agent 會繼承
  那個工作階段為它所執行的指令設定的環境變數，其中一個是祕密。不論執行哪個 agent、使用哪種
  transport，AgentAPI 都會移除它們：Claude Code 的（`CLAUDECODE`、`CLAUDE_CODE_ENTRYPOINT`、
  `CLAUDE_CODE_SESSION_ID`、`CLAUDE_CODE_CHILD_SESSION`、`CLAUDE_CODE_SESSION_ATTENDED`、
  `CLAUDE_CODE_EXECPATH`、`CLAUDE_CODE_MESSAGING_SOCKET`、`CLAUDE_CODE_MESSAGING_TOKEN`、
  `CLAUDE_CODE_SSE_PORT`、`CLAUDE_PID`、`CLAUDE_EFFORT`、`CLAUDE_AGENT_SDK_VERSION`、
  `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS`、`CLAUDE_CODE_SDK_READS_SESSION_STATE`）、
  Codex 的（`CODEX_THREAD_ID`、`CODEX_SANDBOX`、`CODEX_SANDBOX_NETWORK_DISABLED`、
  `CODEX_CI`、`CODEX_INTERNAL_ORIGINATOR_OVERRIDE`、`CODEX_TUI_RECORD_SESSION`、
  `CODEX_TUI_SESSION_LOG_PATH`）、`PI_CODING_AGENT` 和 `AI_AGENT`，以及 AgentAPI 自己的
  `AGENTAPI_API_TOKEN`。`CLAUDE_CONFIG_DIR`、`CODEX_HOME`、`PI_CODING_AGENT_DIR` 或
  `ANTHROPIC_*` 這類設定會保留。

### 登入

Native 模式使用 agent CLI 自己保存的登入資訊。請以執行 AgentAPI 的同一個使用者帳號，用
CLI 本身登入：可以在聊天介面的[終端機](#終端機)裡登入（登入卡片的 **Run in terminal** 和
`/login` 指令都會在那裡執行登入指令），也可以在那台機器上的任何終端機登入（或在 PTY 模式下
登入一次）。

- Claude Code：`claude auth login`（預設使用 Claude 訂閱，`--console` 則改用 Anthropic
  Console 的 API 計費）。在沒有瀏覽器的機器上，`claude setup-token` 會建立一個給
  `CLAUDE_CODE_OAUTH_TOKEN` 使用的 token。`claude auth status` 會顯示登入狀態。
- Codex：`codex login`（ChatGPT 方案），使用 API 金鑰時則用
  `printenv OPENAI_API_KEY | codex login --with-api-key`。`codex login status` 會顯示
  登入狀態。
- Pi：在 `pi` 裡執行 `/login`，或在 AgentAPI 的環境中設定供應商金鑰，例如
  `ANTHROPIC_API_KEY`。

AgentAPI 啟動 agent 時會先檢查登入：Claude Code 用 `claude auth status --json`（API
金鑰、`CLAUDE_CODE_OAUTH_TOKEN`、`apiKeyHelper`、Bedrock 與 Vertex 都算已登入；使用
`--settings` 時會略過這項檢查，因為 `auth status` 看不到用這種方式提供的 `apiKeyHelper`），Codex
用 app-server 的 `account/read`，Pi 則看它的狀態（沒有模型，也沒有可用的模型）。agent
尚未登入時，AgentAPI 不會結束，而是繼續執行：`GET /status` 和 `status_change` 會回報
`login_required`（哪個 agent、哪裡有問題、要執行的指令、更多說明，以及要在哪台主機、
以哪個使用者執行），聊天介面會在輸入框上方顯示一張附有指令和複製按鈕的卡片，這段期間
送出的訊息會在佇列中等候。AgentAPI 每 5 秒重新啟動一次 agent，所以會自己發現你已經
登入；聊天介面的「Check again」（`POST /login/retry`）則會立刻再檢查一次（正在進行的啟動
就是那次檢查）。agent 啟動時印出的警告只會顯示一次，不會每次重試都顯示。如果檢查本身
失敗（較舊的 CLI、包裝程式），agent 會照常啟動。

agent 執行期間登入也可能失效（token 過期、金鑰被撤銷）。回合因登入錯誤而失敗時
（`Not logged in`、HTTP 401、`Invalid API key`、無法再使用的 refresh token、Pi 的
`No API key found`，或訂閱登入過期時的 `Authentication failed for "<供應商>"`），同樣會回報
`login_required`。登入之後按「Check again」（`POST /login/retry`）：AgentAPI 會重新啟動
agent 並接續它的工作階段，對話內容會保留。之後有模型完成的回合時也會清除這個狀態（被中斷
的回合，或 agent 自己處理掉的訊息不算）。

### 請求：核准、提問與計畫

工具核准、提問（Claude 的 `AskUserQuestion`、Codex 的 `request_user_input`、Pi 擴充功能
開啟的對話框）和計畫核准（Claude 的 `ExitPlanMode`）都會以 requests 的形式送達：

- `GET /requests` 列出待處理的請求，以及最近關閉的 50 個請求；`GET /requests/{id}` 回傳
  單一請求。每當有請求開啟或關閉，`/events` 都會送出 `request_update` 事件；
  `GET /status` 的 `pending_requests` 是待處理請求的數量。其他 transport 會回傳空清單。
- `POST /requests/{id}/respond` 回覆一個請求：核准和計畫用 `option_id`（請求帶有
  `feedback` 時，可以另外用 `text` 附上回饋），提問則用以問題 ID 為鍵的 `answers`。每個
  請求最多只能回覆一次：請求已不再待處理（已回覆、已撤回或已過期，從 `--state-db` 還原的
  請求也一樣）時回傳 409，答案不符合請求時回傳 400。
- 每個請求都有 `status`（`pending`、`resolved`、`withdrawn`、`expired`）和
  `response_capability`：`live`（agent 正在等待）、`message`（agent 已不再等待；請改用
  新訊息回答）或 `not_resumable`（已經無法回覆：提出請求的 agent 已結束或重新啟動，或已不再
  等待某個核准或計畫）。agent 不再等待某個請求時（撤回請求，或回合結束），提問會變成
  `message`，核准和計畫則變成 `not_resumable`。已經在送達途中的答案，只要 agent 收下仍然
  算數。
- 祕密問題的答案在保存和回報時都會換成 `[redacted]`，只有 agent 會收到原本的答案。
- [人工回覆 API](#人工回覆-api)（`/handoff`）會提供最早的待處理請求，並附上編號選項：輸入
  數字選擇選項，其他文字則用來回答單一提問，或作為拒絕選項的回饋。回覆只會回答當初
  提供的那個請求；如果它在這段期間已被回覆或撤回，回覆結果是 `superseded`，請求無法
  接受的回覆則是 `invalid`。

在聊天介面中，請求會以決策卡片停靠在輸入框上方。核准請求會顯示指令，選項依作用（允許、
一律允許、拒絕）套用不同樣式，拒絕時可以附上回饋；計畫以 Markdown 顯示，可以核准或附上
回饋退回；提問一題一個步驟（單選或複選、輸入或隱藏的回答），最後一起送出。數字鍵可以選擇
選項，但你正在輸入，或對話框、選單、終端機取得焦點時不會作用，按住數字鍵也不會重複觸發。
工具呼叫那一列會標示它在等待核准、已核准或已拒絕；`AskUserQuestion`、`request_user_input`
和 `ExitPlanMode` 呼叫只能在卡片上回答。在別處回覆或已不再待處理的請求會離開卡片；agent
已不再等待的提問（隨回合過期，或被 agent 撤回）會提示你仍可以用一般訊息回答。有待處理的
請求時，標頭、分頁標題、分頁圖示、embed 狀態列和通知都會顯示「Needs you」。

### Steer、interrupt 與佇列

- `POST /message` 接受 `dispatch_mode`：`queue`（預設值，所有 transport 都一樣：等 agent
  閒置時再送出）、`steer`（把訊息加進 agent 正在進行的回合；回應會標示 `steered`）或
  `interrupt`（停止那個回合，接著送出這則訊息）。它們需要 native transport；有回合在執行時
  還需要對應的能力，否則回傳 400。沒有回合在執行時（包括 agent 正在重新啟動或等待登入），
  兩者都會照常送出或排入佇列。
- `POST /interrupt` 停止執行中的回合（沒有回合在執行，或沒有 agent 在執行時回傳 409）。
- `POST /queue/{id}/steer` 把佇列中的訊息移進執行中的回合（沒有回合在執行時回傳 409，
  訊息仍留在佇列中）。
- `PUT /queue` 以 `{"ids": [...]}` 重新排列佇列，必須把每則排隊中的訊息各列出一次（否則
  回傳 409）。所有 transport 都可以使用。
- agent 拒絕的排隊訊息最多會重試 5 次，之後就丟棄，並以引用該訊息的錯誤告知。agent 沒有及時
  回應的訊息則會立刻丟棄：它可能已經送達，再送一次可能讓任務執行兩次。

`GET /status` 會回報 `capabilities`：`steer`、`interrupt`、`resume`（AgentAPI 再次啟動時
可以接續工作階段，見[持久保存的工作階段](#持久保存的工作階段)）、`settings`（見
[工作階段設定](#工作階段設定)）、`compact`（見[斜線指令與壓縮](#斜線指令與壓縮)）和
`images`（附加的圖片，
也就是指向 10 MB 以內 PNG、JPEG、GIF 或 WebP 檔案的 `@"<路徑>"`，除了路徑之外也會以圖片
送給 agent；不支援圖片的 agent 只會收到路徑）。沒有 agent 在執行時（重新啟動、等待登入），
會回報上一個 agent 支援的能力。

在聊天介面中，agent 工作中時，送出按鈕的選單除了排入佇列（Enter），還有 **Steer**
（⌘/Ctrl+Enter）和 **Interrupt & send**（⌘/Ctrl+Shift+Enter），Stop 則會中斷目前的回合
（`POST /interrupt`）。佇列中的任務可以立即 steer 進去，也可以上下移動（所有 transport
都可以）。

### 工作階段設定

agent 的權限模式、模型和推理強度可以在它執行中切換。`GET /settings` 會回傳目前的設定和可選
的值（agent 提供的權限模式、它的模型以及各模型的推理強度），`PUT /settings` 則會修改 body 裡
有的設定，例如 `{"permission_mode": "ask"}` 或 `{"model": "sonnet", "effort": "high"}`；
設定不接受的值會回傳 400。目前的設定也包含在 `GET /status` 和 `status_change` 裡。

權限模式有 **bypass**（工具直接執行、不詢問；預設值）、**auto**（由 agent 自己的審查決定
什麼可以執行）、**ask**（需要核准的工具呼叫會以請求送來）和 **plan**（agent 先提出計畫，
以計畫請求送來，核准後才動手）：

| 模式 | Claude Code | Codex | Pi |
| --- | --- | --- | --- |
| bypass | `bypassPermissions` | 不核准、不使用沙箱 | Pi 一向如此 |
| auto | `auto` | 工作區沙箱，升級權限由 Codex 的 auto review 決定 | — |
| ask | `default` | `untrusted`：不信任的指令都會詢問 | — |
| plan | `plan` | plan collaboration mode | — |

agent 自己的模式（例如核准計畫後 Claude Code 的 `acceptEdits`、命令列指定的 Codex 政策）
會顯示為沒有模式。Claude Code 和 Pi 會立即套用變更；Codex 則在下一個 turn 套用，因為
app-server 只在那時接受設定。選擇的設定會再套用到取代目前 agent 的新 agent：重新啟動後、
重新登入後，以及使用 `--state-db`（會保存設定）時 AgentAPI 重新啟動後。agent 自己變更的
設定（例如核准計畫後切換的模式）也會跟著更新。

在聊天介面中，輸入框的工具列有一個模式選單，以及一個模型與推理強度的選單（手機上會放在按鈕
上方的獨立一列）。

### Context 與用量限制

`GET /status` 和 `status_change` 會回報 agent 的 `usage`：上一個 turn 之後它的 context
window 用了多少（`context`：以 token 計的 `used` 和 `size`），以及帳號的用量限制
（`rate_limits`：名稱、已用多少、何時重置，Claude Code 另外會說明是否接近或已達上限）。
Claude Code 會在每個 turn 之後回報它的 5 小時與每週限制和 context，Codex 回報它的 context
和用量窗口，Pi 回報它的 context。新的工作階段 context 會歸零；用量限制屬於帳號，會保留。
在聊天介面中，輸入框工具列上的圓環顯示 context 用了多少，它的選單則列出各項限制和重置倒數。

### 終端機

Native agent 沒有自己的終端機，所以 AgentAPI 會在 agent 的工作目錄執行一個 shell，給聊天
介面的終端機按鈕使用：登入、git 指令或其他任何事情都可以。它是真正的終端機（顏色、游標、
全螢幕程式都可以用），會配合視窗大小，在手機上也一樣，並有一排按鍵列補上螢幕鍵盤沒有的按鍵。
所有開啟它的人共用同一個 shell，就像 tmux 視窗，沒有人在看時也會繼續執行；shell 結束後，
下次開啟會啟動新的 shell。它的環境變數和 agent 相同，但不含 AgentAPI 自己的變數。

- `GET /terminal` 以 server-sent events 串流它的輸出（`output` 事件帶 base64 資料，第一個
  事件是最近的輸出，供重播；shell 結束時送出 `exit`），shell 沒有在執行時會啟動它。
- `POST /terminal/input`（`{"data": "..."}`）輸入按鍵，`POST /terminal/resize`
  （`{"cols": 120, "rows": 40}`）調整大小。
- 使用 native transport 時預設開啟，`--terminal=false` 可以關閉。它和 API 的其他部分受到相同
  的保護，所以伺服器可以被別人連到時，請使用 `--api-token`。

### 斜線指令與壓縮

agent 的終端機介面不在，無法執行它的斜線指令，所以聊天介面會替 native agent 自己處理這些指令：

- `/model [模型]`、`/effort [強度]`（`/think`）和 `/mode [模式]`（`/permissions`）依 id 或
  名稱切換設定，不帶參數則開啟對應的選單；`/plan` 切換到 plan 模式；
- `/compact` 壓縮 context；
- `/clear`（`/new`）開始新的對話；
- `/terminal` 開啟終端機，`/login` 在終端機裡執行 agent 的登入指令；
- `/help` 列出這些指令。

輸入指令名稱時，輸入框會提示可用的指令，按 Tab 會補完第一個。其他內容會原樣送給 agent，所以
agent 自己的指令（Claude Code 的 `/code-review`、Pi 擴充功能的指令）仍然可以使用。
`POST /compact` 會在 agent 沒有在執行回合時壓縮它的 context：Codex（`thread/compact/start`）
會用一個獨立的 turn 壓縮，Pi（它的 `compact` 指令）會在完成後才回覆。壓縮期間送出的訊息會在
佇列中等待；agent 拒絕壓縮時（例如 Pi 的「Nothing to compact」）會回傳 409 並附上原因。
Claude Code 則用 `/compact` 訊息壓縮，聊天介面會把這則訊息送給它。

### 持久保存的工作階段

`--state-db <檔案>` 會把對話保存在一個 SQLite 檔案裡：訊息、結構化訊息（工具呼叫、
思考）、時間軸、請求和佇列，以及 agent 自己的工作階段 ID。AgentAPI 用同一個檔案再次啟動
時，聊天介面會顯示原本的對話，排隊中的訊息仍在佇列裡，agent 也會接續它的工作階段，所以它
也記得先前的對話。正常關閉後再啟動時，對話的 epoch 不變，瀏覽器的快取也仍然有效。沒有
`--state-db` 時，AgentAPI 每次啟動都是新的 agent 工作階段。

```bash
agentapi server --transport native --state-db ~/.agentapi/myproject.db -- claude
```

- AgentAPI 停止時尚未回覆的請求會標示為已過期：提出請求的 agent 已經不在了。
- 上傳的檔案會保存在資料庫旁（`<檔案>.uploads`），因為訊息是用路徑引用它們。
- 一個檔案只屬於一種 agent 類型、transport 和工作目錄；用到別人的檔案時，AgentAPI 會拒絕啟動。
  AgentAPI 執行期間會鎖住檔案，所以第二個 AgentAPI（或 `sqlite3`）無法開啟它。
- 新建立的檔案只有擁有者能讀取（0600），缺少的目錄也會一併建立（0700）。
- 重新啟動（Explorer → Restart 或 `DELETE /messages`）會清除保存的對話和佇列，並開始新的
  工作階段。MCP 端點加上 `?restart=true` 則會讓 agent 接續原本的工作階段重新啟動，對話會保留。
  不論哪一種，為工作階段選擇的設定都會保留。
- 寫入會每 200 毫秒批次處理一次，所以串流不必等待磁碟。當機後最後一批寫入可能遺失，所以對話會
  換成新的 epoch，用戶端會重新載入完整的對話。持續失敗的寫入會以錯誤回報。
- `--state-db` 需要 `--transport native`。使用 PTY transport 時 agent 無法接續工作階段；
  `--state-file` 仍會在那裡保存從畫面解析出的訊息。Native transport 不接受 `--state-file`。

### Claude Code

AgentAPI 會以 headless 模式執行 `claude -p`，stdin 和 stdout 都使用 stream-json。工具
呼叫會帶有輸入、結果、狀態和圖片，每則 assistant 訊息都有模型、停止原因和 token 用量。

權限模式、模型和推理強度透過 Claude Code 的 control request（`set_permission_mode`、
`set_model`、`apply_flag_settings`）切換；可選的模型和推理強度來自它的 initialize 回應，
目前使用的模型和推理強度則來自 `get_settings`。AgentAPI 會加上
`--allow-dangerously-skip-permissions`，少了它 Claude Code 會忽略切換到
`bypassPermissions` 的要求；Claude Code 不允許該模式的情況（在沙箱外以 root 執行）除外。
只要工作階段可以切換到 bypass，核准計畫時就會提供「Yes, and bypass permissions」。

- 核准提供 Allow、Deny（可附上給 Claude 的回饋），以及在 Claude Code 建議了權限規則時的
  「Allow always」，這個規則只套用在這個工作階段。Claude Code 認定為唯讀的指令不會詢問就
  直接執行。
- 問題會列出 Claude 提供的選項；其他答案也會被接受。
- 計畫會顯示計畫內容，並提供「Yes, and bypass permissions」（工作階段可以切換到 bypass
  時）、「Yes, and auto-accept edits」、「Yes, and manually approve edits」和「No, keep
  planning」。選 No 時附上的回饋會讓 Claude 修改計畫後再詢問一次。

Claude Code 支援 `steer`（steer 的訊息會在 Claude 的下一步加入正在進行的回合）、
`interrupt`（Claude Code 在 10 秒內沒有結束被中斷的回合時，AgentAPI 會結束它）、`resume`
和 `images`。工作階段名稱是 Claude Code 在第一個提示之後產生的標題；`/clear` 之後的新工作
階段也會取得標題，而 `--state-db` 接續的就是這個新工作階段。回合會在 Claude Code 回報閒置時
結束，所以背景子 agent 以及它完成時喚醒的回合，都屬於
啟動它的那個回合；子 agent 自己的步驟不會出現在對話中（和工作階段紀錄檔一樣），它的工具
呼叫在它回報之前會顯示為執行中。

命令列與環境變數：

- AgentAPI 會加上 `-p --output-format stream-json --input-format stream-json --verbose
  --include-partial-messages --replay-user-messages --permission-prompt-tool stdio`，
  新的工作階段加上 `--session-id`，恢復的工作階段加上 `--resume`。
- 除非你另有指定，否則會加上 `--permission-mode bypassPermissions`，工具會直接執行、
  不會詢問；提問（AskUserQuestion）和計畫（ExitPlanMode）仍然會以請求的形式出現，
  核准計畫時也可以選擇略過權限。想以其他模式啟動，請加上 `--permission-mode default`
  （或 `acceptEdits`、`plan` 等），也可以在 Claude Code 執行中切換（見
  [工作階段設定](#工作階段設定)）；AgentAPI 也會加上 `--allow-dangerously-skip-permissions`，
  讓工作階段之後可以切換回 bypass。Claude Code 不允許在沙箱外以 root 使用
  `bypassPermissions`，所以 AgentAPI 以 root 執行時會改加 `--permission-mode default`、
  不加 `--allow-dangerously-skip-permissions`，並記錄一則警告；在沙箱中請設定 `IS_SANDBOX=1`，或自己指定模式
  （單獨使用 `--allow-dangerously-skip-permissions` 仍會讓 Claude Code 以 root 執行時直接
  結束）。另外也會加上 `--thinking adaptive --thinking-display summarized`（少了它思考內容會
  是空的）。你自己的
  `--permission-mode`、`--dangerously-skip-permissions`、`--thinking`、
  `--max-thinking-tokens` 或 `--thinking-display` 優先。
- 和協定或 AgentAPI 管理的工作階段衝突的參數會在啟動時被拒絕：`-p`/`--print`、
  `--output-format`、`--input-format`、`-r`/`--resume`、`-c`/`--continue`、
  `--session-id`、`--fork-session`、`--from-pr`、`--teleport`、
  `--permission-prompt-tool`、`--permission-prompts`、`--bg`/`--background`、
  `--cloud`/`--remote`、`--desktop`、`--remote-control`/`--rc`、`--sdk-url`、
  `--resume-session-at` 和 `--resume-drops-turn`，寫在短旗標群組裡（例如 `-pc`）也一樣。
  你自己傳入的 `--verbose`、
  `--include-partial-messages` 和 `--replay-user-messages` 會被移除。其他參數
  （`--model`、`--allowedTools`、`--mcp-config`、`--append-system-prompt` 等）都會原樣
  傳遞。
- 除了移除上層 agent 工作階段的環境變數（見 [Native transport](#native-transport)），
  AgentAPI 還會設定 `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` 和
  `CLAUDE_CODE_ENTRYPOINT=sdk-cli`。

恢復工作階段會保留 Claude 的上下文；Claude Code 不會重播先前的訊息，所以聊天介面只能從
`--state-db` 檔案顯示它們。工作階段在有過一個回合之後才能恢復；使用
`--no-session-persistence` 時則無法恢復，`resume` 為 false。Claude Code 結束或重新啟動時
仍未處理的 requests 就無法再回答。需要互動式介面的斜線指令無法運作。

### Codex

AgentAPI 會啟動 `codex app-server`，並開始或恢復一個 thread。訊息、結構化訊息和時間軸的
形狀和工作階段紀錄檔解析器產生的相同，因此聊天介面會即時串流顯示 Codex 的回覆、推理摘要、
指令（指令本身，而不是 `bash -lc` 包裝）、檔案編輯、MCP 工具呼叫、計畫更新和 token 用量。
每個 turn 都會要求詳細的推理摘要，除非你用 `-c` 自己指定 `model_reasoning_summary`；模型
會不會寫出摘要，取決於模型和推理強度。

`codex` 後面的命令列參數會這樣處理：

- `-c`/`--config`、`--enable`、`--disable`、`--code-mode-host`、`--strict-config` 和
  `--analytics-default-enabled` 會傳給 `codex app-server`。
- `-m`/`--model`、`-s`/`--sandbox`、`-a`/`--ask-for-approval`（`untrusted`、`on-request`、
  `never`）和 `-C`/`--cd` 會變成 thread 的設定；`--dangerously-bypass-approvals-and-sandbox`
  等同 `-a never -s danger-full-access`。沒有指定這些（或 `-c approval_policy=...`、
  `sandbox_mode`、`approvals_reviewer`）時，thread 會在沒有核准、沒有沙箱的情況下執行，也就是
  native transport 的 bypass 模式；可以在聊天介面或用 `PUT /settings` 切換（見
  [工作階段設定](#工作階段設定)）。Codex 0.160 不接受設定檔裡的
  `approval_policy = "untrusted"`，要用這個政策請改用 `-a untrusted`。
- `--no-alt-screen`、`--no-daemon` 和 `--stdio` 會被忽略。
- 短旗標可以像 Codex 一樣直接接上值（`-mo3`、`-m=o3`）；開關旗標帶值
  （`--dangerously-bypass-approvals-and-sandbox=false`）會被拒絕。
- 提示文字、子指令（`app-server` 以外）、`-p`/`--profile`、`-i`/`--image`、`--oss`、
  `--local-provider`、`--search`、`--add-dir`、`--approve-for-me`、
  `--dangerously-bypass-hook-trust`、`--remote`、`--remote-auth-token-env`、`--worktree`、
  `--listen`、`--help`、`--version` 和未知的旗標，會在啟動時被拒絕並說明原因。第一則訊息
  請用 `--initial-prompt`，圖片請附加在訊息上。

Codex 支援 `steer`、`interrupt`、`resume` 和 `images`。指令、檔案編輯和 MCP 工具的核准、
權限請求、MCP 表單 elicitation，以及 `request_user_input` 問題都會變成請求（Codex 會在
plan 模式提出這類問題；codex-cli 0.160 可以用 `--enable default_mode_request_user_input`
讓 Codex 在預設模式也提出）。plan 模式就是 Codex 的 plan collaboration mode，這是
app-server API 的實驗性功能，AgentAPI 會啟用它：Codex 會以一份計畫結束它的 turn，AgentAPI
接著像 Codex TUI 一樣詢問「Implement this plan?」。Yes 選項（`implement_bypass`、`implement_auto`
和 `implement_ask`，進入 plan 前的模式排在最前面）會離開 plan 模式、切換到該權限模式，讓 Codex
在同一個回合裡實作；選 No（`keep_planning`）並附上回饋，會把回饋送回給 Codex 繼續規劃；只選 No
則結束這個回合。其他請求的選項 ID 就是 Codex
自己的決定值（`accept`、`acceptForSession`、`acceptWithExecpolicyAmendment`、`decline`、
`cancel` 等），不會自動核准任何東西，AgentAPI 無法顯示的請求則會被拒絕。指令核准會顯示實際
執行的指令（不含 shell 包裝），Codex 對它的解讀（讀取的檔案、搜尋）放在詳細資訊中。回合
結束時仍未回覆的請求會被拒絕（MCP elicitation 會被取消），所以 Codex 和 MCP 伺服器都不會
一直等待。steer 的訊息會在 Codex 下一次向模型發出請求時加入執行中的 turn
（目前的輸出會繼續串流），對它的回覆會歸在該則 steer 訊息底下。interrupt 會在幾毫秒內結束
turn，被中斷的工具呼叫會顯示為失敗。

工作階段參照是 Codex 的 thread ID。恢復時會接續該 thread 並保留上下文；行程結束時正在執行
的 turn 會以 interrupted 狀態回來，當時待處理的核准會消失，Codex 先前的 turn 也不會載入
AgentAPI 的對話（聊天介面只能從 `--state-db` 檔案顯示它們）。無法恢復的 thread 會換成新的
thread，並顯示警告。Codex 不會自己替 thread 命名；在別處設定的名稱（`thread/name/updated`）
會成為工作階段名稱。Explorer 管理的 MCP 伺服器寫在 `$CODEX_HOME/config.toml`，app-server
啟動時才會讀取；儲存時選擇重新啟動，Codex 會接續它的 thread 重新啟動。

Codex 自己的斜線指令是 TUI 功能；`/model`、`/compact` 以及[斜線指令與壓縮](#斜線指令與壓縮)
中的其他指令由聊天介面自己處理，`/review` 則無法使用。指令輸出串流時只是預覽，Codex 可能
漏掉部分內容，指令結束後會換成完整輸出。

### Pi

AgentAPI 會以 Pi 的 RPC 模式執行它（`pi --mode rpc`，經由 stdin 與 stdout 傳送 JSON）。

- **旗標。** AgentAPI 會加上 `--mode rpc --session-id <id>`（每個新的工作階段都會用新的
  `agentapi-<hex>` ID，重新啟動後也是）。會挑選模式或工作階段、或讓 Pi 直接結束的選項
  （`--mode`、`--print`/`-p`、`--session`、`--session-id`、`--continue`/`-c`、
  `--resume`/`-r`、`--fork`、`--no-session`、`--export`、`--list-models`、`--help`/`-h`、
  `--version`/`-v`）、`@file` 參數、訊息（`--` 之後的，或是 RPC 模式會忽略的單純參數），以及
  子命令（`pi install`、`remove`、`uninstall`、`update`、`list`、`config`、`auth`、`mcp`）
  都會被拒絕，錯誤訊息會指出是哪個選項。只影響 TUI 的 `--tui-mode` 與 `--use-theme` 會連同
  它們的值一起丟棄。其他參數都會原樣傳下去
  （`--provider`、`--model`、`--thinking`、`--session-dir`、`-e`、`--approve`、擴充功能的
  旗標）；Pi 內建選項的 `--flag=value` 會拆成兩個參數，因為 Pi 只認得這種寫法。思考等級請用
  `--thinking` 設定。
- **能力。** 支援 steer（`dispatch_mode: "steer"` 會以帶 `streamingBehavior: "steer"` 的
  prompt 送出）、interrupt（`POST /interrupt` 會送出 `abort`；回合會以中斷結束，不會出現 Pi
  在工具執行中中止時多加的「This operation was aborted」錯誤；Pi 在 10 秒內沒有結束回合時，
  AgentAPI 會結束它）、resume，以及圖片（附加的圖片會以 base64 的 `images` 交給 Pi；除非 Pi
  表示它的模型不接受圖片，否則都會回報這項能力）。
- **訊息。** 文字與思考內容會在抵達時串流進 rich messages，bash 的輸出會串流進工具結果，
  最終訊息的形狀和從 Pi 工作階段紀錄檔解析出來的相同。工具結果中的圖片（Pi 讀取的圖片檔）
  也會保留。
- **請求。** 原版 Pi 沒有工具核准。擴充功能開啟的對話框（`select`、`confirm`、`input`、
  `editor`）會變成請求：工具執行中出現的 `select` 或 `confirm` 是對那次工具呼叫的核准（選項 ID
  就是擴充功能給的標籤），所有 `confirm` 都用 `yes`/`no` 回答，其他對話框則是問題（`choice` 或
  `value`；選項 `cancel` 會關掉對話框）。Pi 逾時的對話框會被撤回。回合結束時仍開著的對話框
  會隨回合過期，和其他 agent 的請求一樣（提問之後仍可用訊息回答），擴充功能會收到 cancel；
  中斷正在等待對話框的擴充功能命令時，對話框會被撤回。沒有經過模型回合的擴充功能命令會以
  停止原因 `handled` 結束。擴充功能以 error 等級發出的
  `notify` 會顯示為錯誤，它的 `setTitle` 會在 Pi 還沒有名稱時暫代工作階段名稱；Pi 自己的名稱
  （`--name`、重新命名）就是工作階段名稱。
- **續接。** Pi 的工作階段 ID 就是工作階段參照。續接時會用同一個 `--session-id` 啟動 Pi
  （請維持相同的目錄與 `--session-dir`），所以 Pi 會保留上下文；Pi 不會重播先前的訊息，所以
  聊天介面只能從 `--state-db` 檔案顯示它們。
- **關閉。** 停止 AgentAPI（或重新啟動）時會先中止正在執行的回合，這樣工作階段紀錄檔就不會
  停在一個沒有結果的工具呼叫上，接著依 [Native transport](#native-transport) 所述停止 Pi。
- **設定。** Pi 沒有權限模式。它的模型（`set_model`，來自它有供應商的模型）和思考等級
  （`set_thinking_level`，各模型支援的等級）可以切換；換模型時 Pi 會自己調整等級。
- **命令。** 無法使用 Pi 的 TUI 命令（`/settings`、`/login` 等）；`/model`、`/compact` 以及
  [斜線指令與壓縮](#斜線指令與壓縮)中的其他指令由聊天介面處理，擴充功能的斜線命令可以使用。
  可以在聊天介面的終端機裡登入（執行 `pi`，再輸入 `/login`）。

### 限制

- agent 沒有自己的終端機。聊天介面會隱藏 TTY mode、終端機提示卡片、即時畫面尾端和
  「Terminal output」（ACP 也是，而且 ACP 也不提供 Stop 和工具呼叫上的回答按鈕），`raw`
  訊息（送給終端機的按鍵）會以 400 拒絕，需要 agent 互動式介面的斜線指令也無法運作，聊天介面
  自己處理的指令除外。[終端機](#終端機)是另一個獨立的 shell。
- 重新啟動（Explorer → Restart 或 `DELETE /messages`）會換成新的 agent 並開始新的工作階段，
  同時清除對話和佇列；尚未回覆的請求會變成 `not_resumable`。`--initial-prompt` 會再送給新的
  工作階段，Codex 和 Pi 也會先更新，和 PTY 一樣。
- 沒有 `--state-db` 時，AgentAPI 下次啟動就是新的工作階段，不會接續 agent 先前的工作階段。
- 圖片只能以訊息附件（`@"<路徑>"`）的形式交給 agent，不能用命令列旗標（`codex -i` 和 Pi
  的 `@file` 都會被拒絕）。

## PTY transport

預設的 transport 適用於所有有終端機介面的 agent。對 Claude Code、Codex 和 Pi，它也會追蹤
它們的工作階段紀錄檔來取得結構化活動，聊天介面也能回答它們的終端機對話框。

### 運作原理

AgentAPI 會執行一個記憶體內的終端機模擬器。它把 API 呼叫轉換成對應的終端機按鍵輸入，並把 agent 的輸出解析成一則一則的訊息。

#### 把終端機輸出切分成訊息

訊息分成 2 種：

- 使用者訊息：由使用者傳給 agent
- Agent 訊息：由 agent 傳給使用者

為了從終端機輸出中解析出個別的訊息，我們採取以下步驟：

1. 在使用者送出任何訊息之前，初始的終端機輸出會被視為 agent 的第一則訊息。
2. 當使用者透過 API 送出訊息時，會在送出任何按鍵之前，先擷取一次終端機快照。
3. 接著把使用者訊息送給 agent。從這時開始，每當終端機輸出有變化，就會擷取新的快照，並和初始快照比對差異，出現在初始內容下方的新文字就會被視為 agent 的下一則訊息。
4. 如果在使用者送出新訊息之前，終端機輸出又有變化，就會更新這則 agent 訊息。

這樣就能把終端機輸出切分成一連串的訊息。

#### 從 agent 訊息中移除 TUI 元素

每則 agent 訊息都包含一些對終端使用者沒有用處的多餘內容：

- 訊息開頭的使用者輸入。Coding agent 常常會把輸入內容回顯給使用者，讓它顯示在終端機上。
- 訊息結尾的輸入框。這是使用者平常輸入內容的地方。

AgentAPI 會自動移除這些內容。

- 對於使用者輸入，我們會移除包含使用者上一則訊息文字的那幾行。
- 對於輸入框，我們會在訊息結尾尋找包含常見 TUI 元素（例如 `>` 或 `------`）的行。

#### 如果 Claude Code、Goose、Aider 或 Codex 更新了它們的 TUI，會怎麼樣？

把終端機輸出切分成一連串訊息的部分應該仍然可以運作，因為它不依賴 TUI 的結構。移除多餘內容的邏輯可能需要更新，以處理新的元素。AgentAPI 仍然可以使用，但 agent 訊息中可能會出現一些多餘的 TUI 元素。

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

對於其他 agent，或是拿不到工作階段紀錄檔的環境，仍然會退回使用終端機解析。使用
[native transport](#native-transport) 時，這些端點的內容改由 agent 的協定提供，而不是它的
工作階段紀錄檔。

### 互動式提示支援

聊天介面會偵測互動式 TUI 提示 —— 例如 Claude Code 的計畫核准對話框或權限確認、Codex
的核准請求，以及 Pi 的專案信任提示 —— 並把它們以決策卡片（decision card）的形式固定在
輸入框上方。按數字鍵即可選擇選項。多選題（Claude Code 帶有 `multiSelect` 的
AskUserQuestion）會顯示核取方塊和一個 Continue 按鈕，按下後會前往下一題或檢查步驟。
agent 回答中的編號清單不會被誤判為選項。`GET /status` 會以 `terminal_prompt` 回報該提示。
使用 [native transport](#native-transport) 時沒有終端機提示；核准、提問和計畫會改以結構化
的請求送達。

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

若要透過 Pi 的 RPC 模式、而不是它的終端機介面來驅動 Pi，請參閱
[Native transport](#native-transport)。

## 對話 API

這些端點所有 transport 都能使用；[HTTP API 參考](#http-api-參考)列出了全部的端點。

### 訊息與狀態

- `POST /message` 傳送訊息：`{"content": "Hello, agent!", "type": "user"}`。回傳 200 表示
  AgentAPI 已偵測到 agent 開始處理，或是在 agent 忙碌時把它排入佇列（`queued`）；
  `dispatch_mode` 決定 agent 忙碌時怎麼處理（見 [Steer、interrupt 與佇列](#steerinterrupt-與佇列)）。
  `"type": "raw"` 則是把按鍵輸入 agent 的終端機（PTY transport）。
- `GET /messages` 回傳對話；`GET /rich-messages` 和 `GET /timeline` 回傳結構化的形式（見
  [Claude、Codex 與 Pi 的結構化活動](#claudecodex-與-pi-的結構化活動)）。
- `DELETE /messages` 清除對話（訊息、結構化訊息、請求、時間軸、錯誤和佇列），並重新啟動
  agent（native transport 會開始新的工作階段）。
- `GET /status` 回傳向下相容的 `stable`/`running` 狀態、詳細的生命週期（`starting`、
  `ready`、`running`、`restarting`、`exited` 或 `failed`）、transport、工作階段 ID、單調遞增
  的執行 ID、對話的 `epoch`、工作階段名稱、AgentAPI 版本、agent 目前開啟的對話框
  （`terminal_prompt`）以及終端機寬度（`terminal_columns`）。使用 native transport 時還會
  回報 `capabilities`、`pending_requests`、`settings`、`usage`、`terminal` 和
  `login_required`。
- `GET /title` 回傳工作階段的可讀標題。
- `POST /upload` 儲存檔案（最大 10 MB）並回傳路徑，訊息以 `@"<路徑>"` 引用它。

### 訊息佇列

在 agent 忙碌時送出的訊息會放進 FIFO 佇列，而不是直接被拒絕。排隊中的訊息在送出前，
可以透過 API 或聊天介面查看、編輯或刪除。

- `GET /queue` 列出排隊中的訊息。
- `PUT /queue/{id}` 編輯、`DELETE /queue/{id}` 移除一則排隊中的訊息。
- `PUT /queue` 以 `{"ids": [...]}` 重新排列佇列，必須把每則排隊中的訊息各列出一次。
- `POST /queue/{id}/steer` 把排隊中的訊息移進 agent 正在進行的回合（native transport）。

### 事件與增量同步

`GET /events` 以 server-sent events 串流事件：`message_update`、`status_change`、
`rich_message_update`、`agent_error`、`request_update`（native transport），以及每 15 秒
一次的 `heartbeat`，讓用戶端能察覺連線已經失效。加上 `?sync=1` 時，串流會以 `session_sync`
事件開頭，每次更新都會帶有 `seq`；用 `&since=<seq>&epoch=<epoch>` 重新連線時，只會重播有
變動的部分。

加上 `?sync=1` 時，事件也會帶有 SSE id（重播的最後一個事件是重播的 `seq`，之後的變更
各自帶自己的 `seq`），所以一般的 `EventSource` 自動重新連線時，會從它的 `Last-Event-ID`
接續；請把 `GET /status` 回傳的 `epoch` 放進網址。有訊息被收回時（agent 拒絕開始的回合），
對話會換成新的 epoch，已連線的用戶端會收到 `full: true` 的 `session_sync`，接著是完整的狀態。

## 整合

### 執行狀態 webhook

每當執行狀態在 `running` 和 `stable` 之間切換時，AgentAPI 可以送出 HTTP POST。
Webhook 可以用 CLI 旗標或 `AGENTAPI_WEBHOOK_*` 環境變數初始化，之後再透過
`GET/PUT /webhook` 查看或修改。

傳送是非同步進行的，逾時時間和重試次數都可以設定。你也可以選擇提供 Go `text/template`
payload 範本，依照接收端的需求調整 POST 內容 —— 可用的欄位有 `.ID`、`.Type`、`.CreatedAt`、
`.RunID`、`.Status`、`.PreviousStatus`、`.AgentType` 和 `.Transport`。沒有設定範本時，
會原封不動地送出預設的 JSON payload。

```bash
agentapi server \
  --webhook-url https://example.com/agentapi/events \
  -- claude
```

對應的環境變數有 `AGENTAPI_WEBHOOK_URL`、`AGENTAPI_WEBHOOK_PAYLOAD_TEMPLATE`、
`AGENTAPI_WEBHOOK_TIMEOUT` 和 `AGENTAPI_WEBHOOK_MAX_ATTEMPTS`。逾時時間預設為 `10s`，
最多嘗試傳送 3 次。

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

### 人工回覆 API

其他用戶端（例如聊天機器人）不必透過網頁介面也能回覆 agent。agent 在等你時，
`GET /handoff` 會回傳它在等的東西，並附上 `id` 和 `kind`：它最新的回應（`message`）、
它正在顯示的終端機提示（`terminal`），或使用 native transport 時最早的待處理請求
（`request`）；否則回傳 `{"request": null}`。

帶著該 `request_id`、一個唯一的回覆 `id` 和 `content` 呼叫 `POST /handoff/reply`，
就能回覆它。對一般回應的回覆會當成一般的使用者訊息送出；對請求的回覆則依
[請求](#請求核准提問與計畫)一節所述回答該請求。終端機提示接受畫面上顯示的選項編號、
`enter` 或 `esc`；提示有文字欄位時（例如 Claude Code 的計畫回饋），也可以輸入自由
文字。`outcome` 為 `applied`、`superseded`（已不再開啟，例如已在網頁介面回覆）、
`invalid`（回覆不符合，仍保持開啟）或 `uncertain`，而且不會被回覆兩次。搭配上面的
執行狀態 webhook，整合服務就能在 agent 停下來時通知你，並把你的回覆傳回來。

### MCP 管理

AgentAPI 執行期間可以管理 Claude、Codex 和 Pi 的 MCP 伺服器。實作上會保留
`.mcp.json`（Claude）、`$CODEX_HOME/config.toml`（Codex）或 `~/.pi/agent/mcp.json`（Pi）
中不相關的設定。

API 和 Session Explorer 支援：

- 讀取或整個取代 MCP 伺服器對照表；
- 新增、更新和刪除個別伺服器；
- 檢查遠端 HTTP 連線狀況，並解析本機 stdio 執行檔；
- 儲存、匯入、匯出和套用可重複使用的 MCP 設定檔（profile）；
- 可選擇重新啟動 agent，讓變更立即生效。

重新啟動 agent 時，AgentAPI 本身和它的 HTTP/SSE 用戶端都會保持連線。PTY agent 會取得
新的行程和工作階段紀錄監看器，但先前記憶體中的對話脈絡不會保留。Native agent 則會接續它的
工作階段重新啟動，對話會保留（agent 正在執行回合時回傳 409，因為重新啟動會中斷它）。從
Explorer 或 `DELETE /messages` 重新啟動前，AgentAPI 會先更新 Codex（`codex update`）和 Pi
（`pi update --self`），讓重新啟動後的 agent 是最新版本；Claude Code 則會自行更新。

## 安全性

AgentAPI 沒有使用者或角色的概念：連得到 API 的人，就能做 agent 能做的任何事。預設不需要
token，在所有網路介面上監聽，只要 `Host` 標頭指向本機（`localhost`、`127.0.0.1`、
`[::1]`）就接受連線。主機檢查能防止網頁透過 DNS rebinding 呼叫它，但擋不住網路上的程式，
因為它們可以送出任何 `Host` 標頭。在其他人連得到這個埠之前，請先把伺服器保護好。

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
HMAC 而不是 token 本身，所以更換 token 會讓所有瀏覽器登出；cookie 名稱也由它而來，所以
同一台主機上 token 不同的伺服器各自保有自己的登入。`DELETE /auth/session` 登出，
`GET /auth/session`（不需要憑證）回報 `auth_required` 與 `authenticated`。cookie 是
SameSite=Strict，其他網站的頁面無法帶著它發出請求。從其他 origin 提供的聊天介面（例如在
3000 埠執行的 `bun run dev`）需要把該 origin 加進 `--allowed-origins`；
`--allowed-origins '*'` 不允許帶 cookie 的請求。不同的埠不算不同的網站，而預設允許的來源
包含給聊天介面開發用的 `http://localhost:3000` 和 `http://localhost:3001`：在共用的機器上，
請把 `--allowed-origins` 設成你實際使用的來源，讓已登入瀏覽器的 cookie 只對它們有效。

### 監聽位址

伺服器預設監聽所有網路介面。用 `--bind`（或 `AGENTAPI_BIND`）可以只監聽一個位址，
例如只監聽 loopback，讓網路上的其他機器無法連線。它接受不含連接埠的 IP 位址或主機
名稱；連接埠仍由 `--port` 指定。

```bash
agentapi server --bind 127.0.0.1 -- claude
agentapi server --bind ::1 -- claude          # IPv6 loopback
AGENTAPI_BIND=127.0.0.1 agentapi server -- claude
```

`--bind` 決定誰能連線；下方的 `--allowed-hosts` 決定連得上的請求可以使用哪些
`Host` 標頭。

### 允許的主機

伺服器預設只接受 host 標頭為 `localhost`、`127.0.0.1` 或 `[::1]` 的請求。如果想在其他地方架設 AgentAPI，可以透過 `AGENTAPI_ALLOWED_HOSTS` 環境變數或 `--allowed-hosts` 旗標修改。主機只能填主機名稱（不含連接埠）；伺服器在授權時會忽略傳入請求中的連接埠部分。這只檢查 `Host` 標頭；若要讓伺服器完全不對網路開放，請再用 `--bind 127.0.0.1` 只監聽 loopback（見[監聽位址](#監聽位址)）。

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

### 允許的來源

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

### API 用戶端能做什麼

- 讓 agent 執行它能執行的任何事。Native agent 預設以 bypass 權限模式啟動，工具會直接執行、
  不會詢問；Claude Code 會加上 `--allow-dangerously-skip-permissions`（以 root 執行時除外），
  所以用戶端隨時可以把工作階段切換到 bypass；除非命令列指定了政策，Codex 的預設值會覆蓋
  `~/.codex/config.toml` 裡的政策；ACP transport 則會核准所有權限請求。
- 在[終端機](#終端機)裡執行指令（native transport，預設開啟；`--terminal=false` 可以關閉）。
- 透過檔案檢視器的端點讀取 agent 工作目錄中的檔案；工作目錄以外的路徑（包括透過 symlink）
  會被拒絕，diff 也只會讀取 git 儲存庫。HTML 預覽的腳本在 sandbox 中執行，碰不到聊天介面或
  API。
- 上傳最大 10 MB 的檔案；只有圖片會被回傳。
- 修改 MCP 伺服器和 webhook，以及重新啟動 agent。

只要有其他人連得到伺服器，請同時使用 `--api-token` 和 `--bind`，或放在反向代理之後。

## CLI 參考

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

#### 旗標

每個旗標都可以改用環境變數設定：`AGENTAPI_` 加上大寫的旗標名稱，並把 `-` 換成 `_`
（例如 `AGENTAPI_ALLOWED_HOSTS`）。清單在旗標中以逗號分隔，在環境變數中以空白分隔。

| 旗標 | 預設值 | 說明 |
| --- | --- | --- |
| `-t`, `--type` | 由 agent 指令判斷 | agent 類型；見[支援的 agent](#支援的-agent) |
| `-p`, `--port` | `3284` | 監聽的連接埠 |
| `--bind` | 所有網路介面 | 監聽的位址；見[監聽位址](#監聽位址) |
| `--transport` | `pty` | `pty`、`native` 或 `acp`；見 [Transport](#transport) |
| `--experimental-acp` | `false` | 等同 `--transport acp` |
| `--state-db` | | 保存對話、並接續 agent 工作階段的 SQLite 檔案（native）；見[持久保存的工作階段](#持久保存的工作階段) |
| `--terminal` | `true` | 給聊天介面使用、位於 agent 工作目錄的 shell（native）；見[終端機](#終端機) |
| `-s`, `--state-file` | | 保存從畫面解析出的訊息的檔案（PTY） |
| `--load-state`, `--save-state` | 設定 `--state-file` 時為 `true` | 啟動時載入、關閉時保存狀態檔 |
| `-I`, `--initial-prompt` | | 給 agent 的第一則訊息；以管線輸入時從 stdin 讀取 |
| `-W`, `--term-width` | `80` | 模擬終端機的寬度，也是 TTY mode 每行的長度（PTY） |
| `-H`, `--term-height` | `1000` | 模擬終端機的高度（PTY） |
| `--api-token` | 關閉 | 要求 Bearer token；不帶值時會產生隨機 token 並印出；見 [API token 驗證](#api-token-驗證) |
| `-a`, `--allowed-hosts` | `localhost`、`127.0.0.1`、`[::1]` | 接受的 `Host` 標頭，`*` 表示全部；見[允許的主機](#允許的主機) |
| `-o`, `--allowed-origins` | `http://localhost:3284`、`http://localhost:3000`、`http://localhost:3001` | 允許從瀏覽器呼叫 API 的來源，`*` 表示全部；見[允許的來源](#允許的來源) |
| `--webhook-url` | | 執行狀態改變時通知的網址；見[執行狀態 webhook](#執行狀態-webhook) |
| `--webhook-timeout` | `10s` | 每次傳送的逾時時間 |
| `--webhook-max-attempts` | `3` | 傳送的嘗試次數 |
| `--webhook-payload-template` | | POST 內容的 Go `text/template` 範本 |
| `-c`, `--chat-base-path` | `/chat` | 聊天介面的基礎路徑 |
| `--pid-file` | | 寫入伺服器行程 ID 的檔案，給關閉腳本使用 |
| `-P`, `--print-openapi` | | 印出 OpenAPI schema 後結束 |

### `agentapi update`

把 agentapi 二進位檔更新到 GitHub 上的最新發行版。

```bash
agentapi update          # download and install the latest version
agentapi update --check  # only check if an update is available
agentapi update --force  # update even if already at the latest version
```

在取代正在執行的執行檔之前，會先用該版本的 `checksums.txt` SHA-256 清單驗證發行版的
二進位檔。如果清單不存在、格式錯誤，或和下載的檔案不符，就會拒絕更新。

### `agentapi attach`

連接到執行中 agent 的終端機工作階段（PTY transport；ACP 和 native 的 agent 沒有可連接的
終端機）。

```bash
agentapi attach --url localhost:3284
```

以 `--api-token` 啟動的伺服器需要 token：傳入 `--api-token` 或設定 `AGENTAPI_API_TOKEN`。
按下 `ctrl+c` 即可中斷與工作階段的連接。

## HTTP API 參考

伺服器以 OpenAPI 描述它的 API：儲存庫中的 [openapi.json](openapi.json)、執行中伺服器的
`/openapi.json`，以及 `/docs` 的文件介面。伺服器預設在 3284 埠執行。標示 *native* 的端點
需要 native transport。

### 對話

| 端點 | 說明 |
| --- | --- |
| `GET /messages` | 對話中的所有訊息 |
| `POST /message` | 傳送訊息，agent 忙碌時排入佇列；`dispatch_mode` 為 `queue`（預設）、`steer` 或 `interrupt`（*native*） |
| `DELETE /messages` | 清除對話和佇列，並重新啟動 agent（*native*：開始新的工作階段） |
| `GET /rich-messages` | 結構化的對話：思考內容、工具呼叫與結果、用量 |
| `GET /timeline` | 正規化的工作階段事件，可用於匯出或其他介面 |
| `GET /status` | 狀態、生命週期、執行與工作階段 ID、epoch、工作階段名稱、版本、開啟的對話框；*native*：能力、待處理請求、設定、用量、終端機、登入 |
| `GET /title` | 工作階段的可讀標題 |
| `GET /events` | Server-sent events；`?sync=1` 會在重新連線時增量重播 |
| `GET /workspace` | AgentAPI 在 Coder workspace 中執行時，該 workspace 的網址 |

### 佇列

| 端點 | 說明 |
| --- | --- |
| `GET /queue` | 等待送給 agent 的訊息 |
| `PUT /queue` | 重新排列佇列（`{"ids": [...]}`，每則排隊中的訊息各列一次） |
| `PUT /queue/{id}`、`DELETE /queue/{id}` | 編輯或移除一則排隊中的訊息 |
| `POST /queue/{id}/steer` | 把排隊中的訊息移進執行中的回合（*native*） |

### 控制 native agent

| 端點 | 說明 |
| --- | --- |
| `POST /interrupt` | 停止執行中的回合 |
| `GET /requests`、`GET /requests/{id}` | agent 正在等待的請求（核准、提問、計畫），以及最近關閉的請求 |
| `POST /requests/{id}/respond` | 回覆請求：`option_id`，或以問題為鍵的 `answers`，可另附 `text` |
| `GET /settings`、`PUT /settings` | 權限模式、模型和推理強度，以及可選的值；PUT 會修改 body 裡有的設定 |
| `POST /compact` | 壓縮 agent 的 context（Codex、Pi） |
| `POST /login/retry` | 回報 `login_required` 期間，立刻再檢查一次 agent 的登入（不需要登入時回傳 409） |

### 終端機與檔案

| 端點 | 說明 |
| --- | --- |
| `GET /terminal` | 以 server-sent events 送出 shell 的輸出（*native*，除非 `--terminal=false`） |
| `POST /terminal/input`、`POST /terminal/resize` | 在 shell 中輸入按鍵；設定它的大小 |
| `GET /files/content` | agent 工作目錄中的檔案，超過 1 MiB 會截斷 |
| `GET /files/diff` | 檔案相對於 HEAD 尚未 commit 的變更 |
| `GET /files/preview` | 內嵌了引用檔案的 HTML 檔，供有 sandbox 的 iframe 顯示 |
| `GET /files/image` | agent 工作目錄中的圖片檔（點陣圖依內容判斷，以及 SVG） |

### 上傳

| 端點 | 說明 |
| --- | --- |
| `POST /upload` | 儲存檔案（最大 10 MB）並回傳路徑，供訊息以 `@"<路徑>"` 引用 |
| `GET /uploads/{checksum}/{name}` | 上傳的圖片（依內容判斷 PNG、JPEG、GIF、WebP 或 BMP），供聊天介面顯示 |

### 整合端點

| 端點 | 說明 |
| --- | --- |
| `GET /handoff`、`POST /handoff/reply` | [人工回覆 API](#人工回覆-api)：agent 在等什麼，以及對它的回覆 |
| `GET /webhook`、`PUT /webhook` | 讀取或修改[執行狀態 webhook](#執行狀態-webhook) |
| `GET /mcp`、`PUT /mcp` | MCP 伺服器與受管理的設定檔；PUT 會整組取代，`?restart=true` 會重新啟動 agent（*native*：接續原本的工作階段） |
| `POST /mcp/check` | 檢查遠端 HTTP 伺服器，並解析 stdio 執行檔 |
| `POST /mcp/servers`、`PATCH /mcp/servers/{name}`、`DELETE /mcp/servers/{name}` | 新增、更新或移除單一 MCP 伺服器 |
| `GET /mcp/profiles` | 匯出已儲存的 MCP 設定檔 |
| `PUT /mcp/profiles/{name}`、`DELETE /mcp/profiles/{name}` | 匯入、取代或移除設定檔 |
| `POST /mcp/profiles/{name}/apply` | 用設定檔取代目前的 MCP 設定 |

### 驗證

| 端點 | 說明 |
| --- | --- |
| `GET /auth/session` | API 是否需要 token，以及呼叫端是否已驗證（不需要憑證） |
| `POST /auth/session`、`DELETE /auth/session` | 用 token 讓瀏覽器登入（session cookie），或登出 |

聊天介面本身位於 `/chat/`（以及 `/chat/embed`），TTY mode 則從 `/internal/screen` 讀取 agent
的畫面，這個端點不屬於公開的 API。

## 架構

AgentAPI 是單一的 Go 執行檔：一個 HTTP 伺服器（chi，並由 huma 產生 OpenAPI schema），
負責執行一個 agent 行程，並提供嵌入在執行檔裡的聊天介面（Next.js）。每種 transport 都實作
同一個對話介面，並回報給同一個事件發送器，所以 HTTP API、事件串流、佇列和聊天介面在三種
transport 下的行為都一樣。套件、各 transport 的資料流、事件串流、持久保存與安全模型，請參閱
[docs/architecture.zh-TW.md](docs/architecture.zh-TW.md)。

## 開發

```bash
make build      # out/agentapi, with the chat UI embedded
make gen        # regenerate openapi.json and the version
make lint       # Go, TypeScript, shellcheck and actionlint
go test ./...   # Go tests, including the e2e smoke test
```

測試以 `CGO_ENABLED=0` 執行。每次修改 API 都要一併 commit `openapi.json`，而且只能用
`make gen` 重新產生（見 [AGENTS.md](AGENTS.md)）。聊天介面在 [chat/](chat/README.md)：
`bun run dev` 會連到執行中的 AgentAPI 提供介面，
`agentapi server --transport native --type custom -- native-demo` 則會執行一個模擬的
native agent，不需要真正的 agent 就能開發介面。

## 執行穩定性

這個 fork 也修正了許多問題，包括：寬字元的終端機游標追蹤、PTY 生命週期洩漏、並行事件
傳送、訊息追蹤的競爭條件、ACP 關閉流程、TUI 重新繪製造成的殘影、JSONL 監看器的 flush、
Claude delta 串流期間結構化訊息內容區塊的合併，以及 Claude Code 把工作階段轉存到新的 JSONL
時，執行期的工作階段檔案切換。這些變更的目的是讓 AgentAPI 在長時間工作階段、行程替換，
以及瀏覽器或網路暫時中斷時，都能保持穩定。

## 開發藍圖

視使用者回饋而定，我們正在考慮以下功能：

- [支援 MCP 協定](https://github.com/coder/agentapi/issues/1)
- [支援 Agent2Agent 協定](https://github.com/coder/agentapi/issues/2)

## 長期願景

短期來看，AgentAPI 解決的是如何以程式控制 coding agent 的問題。隨著時間推移，我們希望看到主要的 agent 都推出正式的 SDK。有人可能會好奇，到那時候還需要 AgentAPI 嗎？我們認為這取決於 agent 廠商是決定統一採用共同的 API，還是各自堅持自家的專屬格式。

如果是前者，我們會停止維護 AgentAPI，改為推薦官方 SDK。如果是後者，我們的目標是讓 AgentAPI 成為能控制任何 coding agent 的通用轉接層，讓使用 AgentAPI 的開發者不必修改程式碼就能在不同 agent 之間切換。
