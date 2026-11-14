# CLI 參考

[English](cli.md) | 繁體中文

這一頁列出 `agentapi` 的各個指令和旗標；最後的[資源用量](#資源用量)說明執行中的 AgentAPI 要用掉多少
記憶體和 CPU。

## `agentapi server`

啟動 HTTP 伺服器以控制 agent。若要為 agent 加上啟動參數，請將完整指令放在 `--` 分隔符號之後。

```bash
agentapi server -- claude --allowedTools "Bash(git*) Edit Replace"
```

agent 是 Claude Code、Codex 或 Pi（見 [Agent](../README.zh-TW.md#agent)）：

```bash
agentapi server -- codex -a on-request
agentapi server -- pi
```

### 旗標

每個旗標都可以改用環境變數設定：`AGENTAPI_` 加上大寫的旗標名稱，並把 `-` 換成 `_`
（例如 `AGENTAPI_ALLOWED_HOSTS`）。清單在旗標中以逗號分隔，在環境變數中以空白分隔。

也可以寫在 `~/.agentapi/config.yaml`（`--config` 可指定其他檔案），以旗標名稱為 key，套用到這個
使用者啟動的每個伺服器；旗標或環境變數會覆蓋它。不是旗標名稱的設定會記在 log 中；讀不了的檔案會
記下並略過，agent 仍會啟動。[`agentapi hub join`](#agentapi-hub) 會寫入這個檔案；含有 secret 時
請只讓擁有者讀取（`chmod 600`）。檔案或目錄設定開頭的 `~` 代表家目錄，不論設定來自哪裡。
`state-db` 和 `pid-file` 指的檔案只能給一個伺服器使用，所以寫在設定檔時，路徑中都要有 `{port}`
（`{hostname}` 也會填入）；沒有的話，每個伺服器都會略過這個設定並記下警告，旗標或環境變數則照常
有效。

```yaml
webhook-url: http://hub.vpn:3290/hooks
webhook-secret: s3cret
webhook-events: [instance.*, task.*, request.*]
public-url: http://dev-1.vpn:{port}
state-db: ~/.agentapi/state/{port}.db
```

| 旗標 | 預設值 | 說明 |
| --- | --- | --- |
| `-t`, `--type` | 由 agent 指令判斷 | agent 類型：`claude`、`codex`、`pi` 或 `native-demo`；見 [Agent](../README.zh-TW.md#agent) |
| `-p`, `--port` | `3284` | 監聽的連接埠 |
| `--bind` | 所有網路介面 | 監聽的位址；見[監聽位址](security.zh-TW.md#監聽位址) |
| `--state-db` | | 保存對話、並接續 agent 工作階段的 SQLite 檔案；`{port}` 和 `{hostname}` 會填入；見[持久保存的工作階段](agents.zh-TW.md#持久保存的工作階段) |
| `--terminal` | `true` | 給聊天介面使用、位於 agent 工作目錄的 shell；見[終端機](agents.zh-TW.md#終端機) |
| `--auto-continue` | `false` | 回合因用量限制停下時，在限制重置後繼續；見[達到用量限制時](agents.zh-TW.md#達到用量限制時) |
| `--retry-delays` | `30s,1m,2m,5m*12` | 回合因暫時性的 API 錯誤失敗後，每次重試前等多久（`5m*12`：重複 12 次）；`off` 關閉重試；見[暫時性的 API 錯誤](agents.zh-TW.md#暫時性的-api-錯誤) |
| `--prices` | | 每百萬 tokens 模型價格的 JSON 檔，新增或取代內建價格；見[工作階段花費](agents.zh-TW.md#工作階段花費) |
| `-I`, `--initial-prompt` | | 給 agent 的第一則訊息；以管線輸入時從 stdin 讀取 |
| `-W`, `--term-width`, `-H`, `--term-height` | | 已棄用且沒有作用：PTY transport 的終端機大小 |
| `--api-token` | 關閉 | 要求 Bearer token；不帶值時會產生隨機 token 並印出；見 [API token 驗證](security.zh-TW.md#api-token-驗證) |
| `-a`, `--allowed-hosts` | `localhost`、`127.0.0.1`、`[::1]` | 接受的 `Host` 標頭，`*` 表示全部；見[允許的主機](security.zh-TW.md#允許的主機) |
| `-o`, `--allowed-origins` | `http://localhost:3284`、`http://localhost:3000`、`http://localhost:3001` | 允許從瀏覽器呼叫 API 的來源，`*` 表示全部；見[允許的來源](security.zh-TW.md#允許的來源) |
| `--webhook-url` | | webhook 事件 POST 的目的網址；見 [Webhook](api.zh-TW.md#webhook) |
| `--webhook-events` | `run.status_changed` | 要送的事件：類型、群組（`task.*`）或 `*` |
| `--webhook-secret` | | 為每次傳送簽章的 secret（`X-AgentAPI-Signature-256`） |
| `--webhook-heartbeat` | `60s` | `instance.heartbeat` 的間隔；`0` 為關閉 |
| `--instance-id` | `<hostname>:<port>` | 在 `GET /summary` 和每個 webhook 事件中代表這個 AgentAPI 的 ID；見[監看多個實例](api.zh-TW.md#監看多個實例) |
| `--name` | 工作目錄的名稱 | 監看多個 AgentAPI 的用戶端顯示的名稱 |
| `--registry` | `true` | 執行期間在登記目錄保留登記檔；見[監看多個實例](api.zh-TW.md#監看多個實例) |
| `--registry-dir` | `~/.agentapi/instances` | 登記目錄 |
| `--public-url` | | 可以連到這個 AgentAPI 的網址，會放在 `GET /summary` 和每個 webhook 事件中；`{port}` 和 `{hostname}` 會填入，`{ip}` 則填入這台主機通往 webhook 網址那條路由上的 IPv4 位址（沒有 webhook 網址時，取第一個私有網路位址，其次是 CGNAT 位址），執行期間最多每 30 秒重新查詢一次；查不到時沿用上一個位址。`{ip}` 代表的位址一律是允許的主機 |
| `--config` | `~/.agentapi/config.yaml` | 設定檔：以旗標名稱設定，優先順序低於旗標和環境變數 |
| `--allow-update` | `false` | 讓 API 用戶端（例如 hub）把 AgentAPI 更新到最新發行版，再原地重新啟動；見[透過 API 更新](api.zh-TW.md#透過-api-更新) |
| `--webhook-timeout` | `10s` | 每次傳送的逾時時間 |
| `--webhook-max-attempts` | `3` | 傳送的嘗試次數 |
| `--webhook-payload-template` | | POST 內容的 Go `text/template` 範本 |
| `-c`, `--chat-base-path` | `/chat` | 聊天介面的基礎路徑 |
| `--pid-file` | | 寫入伺服器行程 ID 的檔案，給關閉腳本使用；`{port}` 和 `{hostname}` 會填入 |
| `--log-level` | `info` | 記錄的最低層級：`debug`、`info`、`warn` 或 `error` |
| `-P`, `--print-openapi` | | 印出 OpenAPI schema 後結束 |

AgentAPI 把 log 寫到標準輸出，每筆一行 `time=… level=… msg=…`。`--log-level debug`（或
`AGENTAPI_LOG_LEVEL`、設定檔中的 `log-level`）會加上排查問題用的內容，例如 agent 的命令列、登入
檢查，以及成功的讀取（狀態碼低於 400 的 `GET`、`HEAD`、`OPTIONS` 請求；用戶端會輪詢 `GET /status`，
所以 Info 不記這些）；`warn` 和 `error` 則略過例行的紀錄。層級不正確時，伺服器不會啟動。

## `agentapi update`

將 AgentAPI 執行檔更新為 GitHub 上的最新發行版。

```bash
agentapi update            # 下載並安裝最新版本
agentapi update --check    # 僅檢查是否有可用更新
agentapi update --force    # 即使已是最新版本也執行更新
agentapi update --restart  # 更新後讓執行中的 AgentAPI 改用新版本
agentapi update --restart --allow-update  # 之後也讓 hub 可以更新它們
```

更新前，AgentAPI 會先依據該版本的 `checksums.txt` 清單，以 SHA-256 驗證下載的執行檔，
通過後才取代目前的執行檔。若清單不存在、格式錯誤、檢查碼與下載檔案不符，或下載的執行檔執行起來
不是該版本，就會拒絕更新。取代執行檔時，使用的鎖定檔和[透過 API 更新](api.zh-TW.md#透過-api-更新)相同。
查詢發行版最多 1 分鐘；等待鎖定檔、下載與取代執行檔則最多 10 分鐘。

執行中的 AgentAPI 在重新啟動前仍是舊版本。`--restart` 會像
[`agentapi restart --all`](#agentapi-restart) 一樣，重新啟動仍在執行舊執行檔的 AgentAPI；
執行檔已是最新版本時也會重新啟動，因為可能是其他更新取代了它。
透過 `sudo` 執行時（安裝在 `/usr/local/bin`），重新啟動的是執行 `sudo` 那位使用者的 AgentAPI，
依據該使用者的登記目錄。hub 不在其中：請用服務管理工具重新啟動，例如
`systemctl restart agentapi-hub`。

`--allow-update` 會在 `--restart` 之前，先把 `allow-update: true` 寫進設定檔（`--config`，預設是
執行 `sudo` 那位使用者的 `~/.agentapi/config.yaml`，檔案擁有者仍是該使用者），重新啟動後的
AgentAPI 從此就能由 [hub](hub.zh-TW.md#從-hub-更新) 更新。設定檔裡的其他設定都會保留，
和 `agentapi hub join --allow-update` 一樣。搭配 `--check` 時不會寫入任何東西。舊版的 `agentapi`
沒有這個旗標：先自己把這一行加進設定檔，再執行 `agentapi update --restart`。

## `agentapi restart`

原地重新啟動執行中的 AgentAPI，例如在更新取代執行檔之後：

```bash
agentapi restart 3285 3286  # 這些連接埠上的 AgentAPI
agentapi restart --all      # 你在這台主機上的所有 AgentAPI
```

`agentapi restart` 從[登記目錄](api.zh-TW.md#監看多個實例)找到這些 AgentAPI，並送出 SIGUSR2。AgentAPI 會先以
`--version` 執行啟動時的程式，再以之後要用的參數和環境變數執行一次，確認它能以這些設定啟動：
旗標、`AGENTAPI_*` 環境變數和設定檔、agent 的參數，以及它能寫入的狀態資料庫。任一項失敗時（例如
更新帶來的發行版不接受某個設定），記下原因並繼續提供服務，`agentapi restart` 也會回報它仍照原樣
執行。成功時就像關閉那樣停止（中斷並關閉 agent、寫入狀態資料庫、刪除登記檔，webhook 也會送出
`instance.stopped`），再在同一個行程中
以相同的參數和環境變數執行同一個路徑。PID 不變，所以 PID 檔和等待它的上層 shell（例如 Coder
workspace 的啟動腳本）仍然有效，輸出也照舊寫到原本的地方。這個指令會等到每個 AgentAPI 以新的
`server_id` 在原連接埠上回應，並印出新版本。

- 使用 `--state-db` 時，對話會保留，agent 也會接續原本的工作階段。沒有時，agent 同樣會接續
  工作階段：重新啟動會把工作階段的 ID 交給新程式，新程式再從工作階段的紀錄讀回對話，就像從
  [先前的工作階段](agents.zh-TW.md#先前的工作階段)開啟它一樣。僅限 Claude Code、Codex 和 Pi。佇列、待回覆的
  請求、選擇的設定，以及用量限制的停止或重試等待都會重新開始，先前送過的 `--initial-prompt`
  也不會再送出。紀錄已經不在的工作階段會重新開始，聊天介面會顯示警告。從較早的版本更新時，
  那一次重新啟動仍是新的工作階段：較早的版本不會交出工作階段。
- 只會重新啟動你自己的 AgentAPI，而且登記檔必須對應到執行中的 `agentapi server`。透過 `sudo`
  執行時，指的是執行 `sudo` 那位使用者的 AgentAPI。較早的版本無法原地重新啟動，
  `agentapi restart` 會指出來：這一次請自行重新啟動。
- 這個指令若是在某個 AgentAPI 底下執行（例如 agent 執行 `agentapi update --restart`），那個
  AgentAPI 會最後才重新啟動；重新啟動會關閉 agent，這個指令也可能隨之結束。
- `--registry-dir` 可讀取其他登記目錄，`--timeout`（預設 `1m`）限制等待每個 AgentAPI 的時間。
- Windows 不支援。

## `agentapi list`

從[登記目錄](api.zh-TW.md#監看多個實例)列出這台主機上執行中的 AgentAPI，以及每個 `GET /summary` 的內容：
閒置、工作中、需要你處理什麼（`needs request`），以及未讀的任務。

```bash
agentapi list
NAME      AGENT   STATE                  URL                    TITLE
api       claude  needs request, unread  http://localhost:3284  Fix the failing test
frontend  codex   working                http://localhost:3285  Add the settings page
```

`--json` 以 JSON 輸出登記檔和摘要，`--registry-dir` 讀取其他目錄，`--api-token`（或
`AGENTAPI_API_TOKEN`）會送給需要 token 的實例。

## `agentapi hub`

執行監看多個 AgentAPI 的 hub，或把這台主機上的 AgentAPI 接到 hub；見[執行 hub](api.zh-TW.md#執行-hub) 和
[加入 hub](api.zh-TW.md#加入-hub)。

```bash
agentapi hub serve --token "$HUB_TOKEN" --secret "$HUB_SECRET" --instance-token "$AGENTAPI_TOKEN"
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
agentapi hub status
agentapi hub leave
```

`serve` 執行 hub：`--token`（或 `AGENTAPI_HUB_TOKEN`）是登入用的 token（改用 `--no-token` 則不需登入，
適合本身已在另一層登入後面的 hub，例如 Coder workspace app），`--secret`（或
`AGENTAPI_HUB_SECRET`）用來檢查 AgentAPI 的事件，`--instance-token`（或
`AGENTAPI_HUB_INSTANCE_TOKEN`，否則 `AGENTAPI_API_TOKEN`）會送給它們。`--state` 保存實例，
`--port` 和 `--bind` 設定監聽位置，`--allowed-hosts` 是瀏覽器可以用來開啟 hub 的名稱（不設定時任何名稱都可以），`--log-level`（或 `AGENTAPI_HUB_LOG_LEVEL`）設定要記錄多少。
[issue 派工](hub.zh-TW.md#issues)用到的是 `--dispatch-token`（或 `AGENTAPI_HUB_DISPATCH_TOKEN`）：
用戶端（GitHub workflow 的 runner）只能拿它呼叫 `POST /api/dispatch`；以及 `--github-app-file`：
hub 的 GitHub App 存放的檔案（預設是 `--state` 旁的 `github-app.json`）。

`join` 把 hub 的 webhook 寫進 `~/.agentapi/config.yaml`，並套用到正在執行、已登記的 AgentAPI。
`--secret`（或 `AGENTAPI_HUB_SECRET`）為事件簽章，`--events` 選擇事件（預設 `instance.*`，hub 只需要
這些，其餘都從摘要得知），`--public-url` 是 hub 連回它們的網址（`{ip}` 會跟著主機的位址走；
設定檔和 2.1.2 之後的 AgentAPI 保留 `{ip}`，較舊的則拿到當下的位址），
`--api-token`（或 `AGENTAPI_API_TOKEN`）會送給需要 token 的實例，`--config` 和 `--registry-dir`
可指定其他檔案。當下無法修改的 AgentAPI，會在重新啟動時套用設定檔。`--allow-update` 會另外寫入
`allow-update: true`，AgentAPI 重新啟動後，hub 就能[透過 API 更新它們](api.zh-TW.md#透過-api-更新)；`leave` 會保留這個設定。

## `agentapi host`

在已加入 hub 的主機上執行 [issue 派工](hub.zh-TW.md#issues)的 launcher：hub 每派一個 GitHub
issue 到這台主機，它就在該 issue 分支的 git worktree 裡啟動一個 AgentAPI，之後再停掉。整套派工的
架設見 [examples/issue-dispatch](../examples/issue-dispatch/README.md)。

只有要接 issue 的主機才需要 launcher。光是 `agentapi hub join`，主機上的 AgentAPI 就會回報給 hub，
在清單裡看得到、也能開啟。launcher 回報的是主機本身（能跑的 agent、repo、容量和 job），顯示在 Issues
的主機列，並讓 hub 能在這台主機上啟動 AgentAPI；這些 AgentAPI 和其他的一樣會回報。launcher 不需要另外
設定 hub，直接讀 `hub join` 寫入的設定。兩者分開，是因為 launcher 讓 hub 能在主機上啟動程式，加入 hub
則不會。

```bash
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
echo "$INSTANCE_TOKEN" > ~/.agentapi/instance.token && chmod 600 ~/.agentapi/instance.token
agentapi host install --now
agentapi host status
```

| 指令 | 旗標 | 作用 |
| --- | --- | --- |
| `serve` | `--config`、`--log-level`（`info`） | 在前景執行 launcher |
| `status` | `--config` | 顯示 launcher、它的 hub、agent、repo 和 job |
| `install` | `--config`、`--now` | 寫入 `~/.config/systemd/user/agentapi-host.service`（執行 `agentapi host serve`）並啟用；`--now` 會一併啟動，已在執行就重新啟動。再執行一次會重寫 unit，例如程式搬了位置之後 |
| `uninstall` | | 停用、停止並移除這個 service；job 的 AgentAPI 繼續執行，檔案也都保留 |

service 設了 `KillMode=process`：重新啟動或停止 launcher 時，job 的 AgentAPI 繼續執行，由下一個
launcher 接手。`install` 需要 systemd user instance；登出後 user service 會停止時，它會提醒你，
用 `sudo loginctl enable-linger <user>` 讓它們繼續執行。

沒有 systemd 的環境（容器，例如 Coder workspace）裡，`install` 會停在 `no systemd user instance`。
這時照這台主機啟動其他程式的方式，用你自己的身分執行 `agentapi host serve`，不要加 `sudo`
（launcher 讀的是你的 `~/.agentapi/config.yaml`，token 也存在你的 `~/.agentapi`）：

```bash
nohup agentapi host serve >~/.agentapi/host.log 2>&1 &
```

這樣只會維持到 workspace 停止。Coder workspace 可以寫進範本，每次啟動都會執行：

```hcl
resource "coder_script" "agentapi_host" {
  agent_id     = coder_agent.main.id
  display_name = "AgentAPI launcher"
  run_on_start = true
  script       = "nohup agentapi host serve >\"$HOME/.agentapi/host.log\" 2>&1 &"
}
```

launcher 讀 `agentapi hub join` 寫入的 `webhook-url` 和 `webhook-secret`，以及
`~/.agentapi/config.yaml` 的 `host` 區段（`--config` 可指定其他檔案，launcher 的檔案放在它旁邊）：

```yaml
host:
  port: 3291                    # launcher 的 API，hub 會呼叫它
  bind: ""                      # 所有介面；建議設成 VPN 位址
  public-url: http://{ip}:3291  # hub 連回 launcher 的網址
  workdir: ~/work               # clone 和 worktree 的位置
  scan: [~/work, ~/src]         # 找既有 repo 的目錄（深度 2）
  ports: 4100-4199              # job 的 AgentAPI 用的 port
  max-jobs: 4
  agents: [claude, codex]       # 預設為已安裝的（claude、codex、pi）
  agent: claude                 # 派工沒指定 agent 時用的 agent
  clone: true                   # 沒有的 repo 是否自動 clone
```

| 檔案 | 內容 |
| --- | --- |
| `~/.agentapi/host.token` | hub 呼叫 launcher 用的 token，第一次啟動時產生。hub 會把主機名稱綁定到它：換了 token 的 launcher（檔案被刪、主機重灌）要等 hub 移除這台主機後才能加入：在 hub 的 Issues 按主機的 **×**，或呼叫 `DELETE /api/hosts/{name}`（見[疑難排解](hub.zh-TW.md#疑難排解)） |
| `~/.agentapi/instance.token` | 所有 job 的 AgentAPI 共用的 API token，必須等於 hub 的 `--instance-token` |
| `~/.agentapi/host.sock` | job 的 `git` 和 `gh` 向 launcher 拿 GitHub token 的地方 |
| `~/.agentapi/jobs.json`、`~/.agentapi/jobs/` | job 清單，以及各 job 的狀態資料庫、log 和 `gh` shim |

## 資源用量

AgentAPI 本身很輕量：一個 31–34 MiB 的 Go 行程，agent 閒置時用不到單一 CPU 核心的 0.1%。
大部分的成本來自它執行的 agent CLI，約 120–285 MiB。

以下數字是 2026-11-13 量測的：AgentAPI v5.0.0（執行檔 33 MiB）、Claude Code 2.1.302、
codex-cli 0.167.0 和 Pi 1.3.0，在 Linux、4 核心的 Intel Xeon Silver 4210（2.2 GHz）上執行，
並連著一個聊天介面（一個 SSE 用戶端）。每個 agent 跑三個簡短的回合：回覆約 200 個單字、不使用工具。
記憶體是常駐記憶體（RSS）；閒置時的 CPU 是一分鐘內單一核心的使用比例，回合則是整個回合用掉的
CPU 秒數，agent 的所有行程合計。

| Agent | AgentAPI 記憶體 | AgentAPI CPU（閒置） | AgentAPI 每回合 CPU | Agent 記憶體 | Agent 每回合 CPU |
| --- | --- | --- | --- | --- | --- |
| Claude Code | 31 MiB | 0.08% | 0.06–0.08 秒 | 230–285 MiB | 0.5–1.2 秒 |
| Codex | 34 MiB | 0.07% | 0.09–0.13 秒 | 190–265 MiB | 0.3–0.8 秒 |
| Pi | 31–32 MiB | 0.05% | 0.11–0.13 秒 | 120–130 MiB | 0.2–0.3 秒 |
| `native-demo`（沒有 agent 行程） | 31 MiB | 0.05% | 0.01–0.02 秒 | | |

- agent 自己的記憶體會在工作階段中變動：Claude Code 從閒置時的 230 MiB 在幾個回合中增加到
  265–285 MiB；Codex 在第一個回合增加到 265 MiB，之後釋放了約 70 MiB。
- AgentAPI 的程式碼（記憶體中約 17 MiB）由同一個執行檔啟動的實例共用：同時執行四個時，
  每個約占 17–21 MiB（PSS）。
- 和 v0.24.0 的同樣量測（2026-10-18，執行檔 29.5 MiB）相比，AgentAPI 多用約 4 MiB（當時 27–30 MiB），
  CPU 不變；agent 本身的數字變化不大。
- 沒有計入：agent 啟動的 MCP 伺服器、它執行的指令、shell 終端機（開啟時有一個 shell）以及
  瀏覽器分頁。

長對話在狀態資料庫裡每個任務大約增加 100 KB。2026-11-10 在同一台機器上，用 `native-demo` 從
`--state-db` 還原每個任務 100 KB 的對話。AgentAPI 4.2.0 會把整段對話放在記憶體，4.3.0 起只放最近
100 個任務（[長對話](api.zh-TW.md#長對話)）：

| 任務數 | 狀態資料庫 | 記憶體，4.2.0 | 記憶體，4.3.0 | 第一次 `/events?sync=1` 重播，4.2.0 | 4.3.0 |
| --- | --- | --- | --- | --- | --- |
| 0 | | 38 MiB | 34 MiB | 0.6 KB | 0.6 KB |
| 250 | 26 MB | 71–86 MiB | 49–52 MiB | 11 MB | 0.9 MB |
| 1,000 | 105 MB | 193–245 MiB | 55 MiB | 44 MB | 0.9 MB |

剩下會增加的記憶體來自訊息和任務，每個任務幾 KB。訊息會完整留在記憶體裡，很長的提示詞也全部
算在內：2026-11-13 量測一段 1,000 個任務、內容幾乎都是 90 KB 提示詞的對話（狀態資料庫 97 MB），
AgentAPI 5.0.0 用了 148 MiB（250 個任務時 67 MiB），而第一次重播只有最近 20 個任務，維持在 1.8 MB。

規劃主機時，每個工作階段請預留約 35 MiB 給 AgentAPI，再加上 agent 本身（Claude Code 或 Codex
約 350 MiB，Pi 約 150 MiB），以及它的 MCP 伺服器和指令所需的資源。
