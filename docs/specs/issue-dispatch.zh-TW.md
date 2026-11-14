# Spec：Issue 派工（hub + 主機 launcher + 內網 runner）

- 狀態：草稿 v7（階段 1～3 已實作，含 review 後的修正，見 §14）
- 對象：`k1dav-c/agentapi`（以 v3.9.0 為基礎）、`k1dav-c/.github` 的 reusable workflow、`k1dav-c/coder-templates`
- 日期：2026-11-08
- 流程圖：[issue-dispatch-flow.zh-TW.md](issue-dispatch-flow.zh-TW.md)

## 0. 已決定

| # | 問題 | 決定 |
|---|---|---|
| 1 | 用哪個 agent | 由 label 指定：`agent:claude`、`agent:codex`、`agent:pi`；只有 `agent` 時用主機的預設值 |
| 2 | PR 關閉時刪遠端分支 | 要，由 hub 以 App 身分刪除 |
| 3 | token 自動續期 | 要。GitHub App 由 hub 以 manifest flow 建立並保存 private key，主機需要時向 hub 換 token |
| 4 | launcher 怎麼啟動 | Coder template 啟動；不是 template 建的舊主機用 `agentapi host install` 手動裝成 systemd user service，兩者走同一段程式 |
| 5 | 在 hub 建立 issue | 可以：Issues 面板建立 issue 後直接 Assign（§5.6） |
| 6 | 換 agent 重做 | 可以：同一個 issue 換 agent 時，停掉舊的 instance，在同一個分支上以新 agent 接手（§5.7） |
| 7 | 花費彙總 | 要：job 結束時，hub 在 PR（沒有 PR 時在 issue）留言列出每一次執行的花費（§5.8） |

## 1. 目標

把 GitHub issue 交給內網主機上的 coding agent 開發，並在同一個對話裡處理 review：

1. issue 加上 `agent[:<type>]` label，或在 hub 畫面選 issue 並 Assign，主機開 worktree、啟動一個 issue 專用的 AgentAPI instance 開始開發。
2. agent 以 GitHub App 的身分 push 並開 PR，你以 reviewer 身分 review、approve。
3. PR 上的 review 或 `/agent ...` 留言轉送給同一個 instance；你也能直接在 hub 打開那個 instance 對話。
4. PR 關閉後，主機停掉 instance、移除 worktree，hub 刪除遠端分支。

全程不對外開放任何 port：GitHub 事件由內網 self-hosted runner 主動拉取；GitHub App 的設定透過瀏覽器跳轉完成。

### 不做

- 不支援公開 repo 自動觸發（見 §10）。
- 不做跨主機搬移進行中的 job。
- 不取代現有的手動 instance（Coder template 啟動的 3285、3286、3288 不受影響）。
- 不支援 GitHub Enterprise Server（第一版）。

## 2. 名詞

| 名詞 | 意思 |
|---|---|
| hub | `agentapi hub serve`，唯一的調度中心，也保存 GitHub App |
| launcher | 新增的 `agentapi host serve`，每台主機一個，負責開關 issue 專用的 instance |
| job | 一個 issue 的一次開發：一個 worktree、一個分支、一個 instance、一個 state db |
| job key | `<owner>/<repo>#<issue>`，全域唯一；路徑與檔名中寫成 `<owner>~<repo>~<issue>` |
| dispatch | 對 hub 下的指令：`start`、`send`、`link_pr`、`stop` |
| runner | 內網的 GitHub Actions self-hosted runner，只負責把事件轉成 dispatch |

## 3. 架構

```
                 ┌──────── 瀏覽器跳轉（設定 App、安裝）────────┐
                 ▼                                             │
GitHub ──(runner 主動拉 job)──► 內網 runner                    │
  ▲                                │ POST /api/dispatch         │
  │ App 身分：留言、刪分支、        ▼ （dispatch token）         │
  │ 產生 installation token      hub ◄──────────────────── 你（瀏覽器）
  └─────────────────────────────── │ ▲
                     HTTP（host token）│ │ host.* 事件、換 token（簽章）
                                   ▼ │
                               launcher（每台主機）
                                   │ 子行程
                                   ▼
                     agentapi server（每個 job 一個）
                       ├─ git / gh 透過 launcher 拿 token
                       └─ instance.* / task.* 事件照舊回報給 hub
```

- 所有入口（label、留言、review、hub 畫面 Assign）都走同一個 `POST /api/dispatch`。
- issue → 主機 → instance 的對應只存在 hub。
- GitHub 相關的寫入（issue 留言、刪分支）由 hub 以 App 身分執行；workflow 只負責轉送事件，不持有 App 的任何 secret。
- launcher 加入 hub 的方式跟 instance 一樣：讀 `~/.agentapi/config.yaml` 的 `webhook-url`、`webhook-secret`。

## 4. launcher：`agentapi host`

新增 `cmd/host`、`lib/host`。子指令：

| 指令 | 說明 |
|---|---|
| `agentapi host serve` | 前景執行 launcher |
| `agentapi host install [--now]` | 寫入並啟用 systemd user service（§4.9），可重複執行 |
| `agentapi host uninstall` | 停用並移除 service，不動 job 資料 |
| `agentapi host status` | 顯示 launcher 狀態、回報的 hub、repo 與 job |
| `agentapi host git-credential` | git credential helper（§4.5），由 git 呼叫 |
| `agentapi host gh-token` | 印出目前 job 的 token，供 `gh` shim 使用（§4.5） |

### 4.1 設定

放在 `~/.agentapi/config.yaml` 的 `host:` 區段：

```yaml
host:
  port: 3291                     # launcher API
  bind: ""                       # 預設所有介面；建議設內網位址
  public-url: http://{ip}:3291   # hub 連回 launcher 的網址（不能是 loopback、link-local，見 §11 第 10 點）
  token-file: ~/.agentapi/host.token              # hub → launcher；不存在時產生
  instance-token-file: ~/.agentapi/instance.token # 所有 job instance 共用，必須等於 hub 的 --instance-token
  workdir: ~/work                # repo 與 worktree 的根目錄
  scan: [~/work, ~/src]          # 掃描既有 repo 的目錄（深度 2）
  ports: 4100-4199               # job instance 的 port 範圍
  max-jobs: 4
  agent: claude                  # label 只有 `agent` 時用的預設值
  agents: [claude, codex]        # 這台主機能跑的 agent（依已安裝的 CLI 自動偵測，可覆寫；native-demo 只能明列）
  agent-commands:                # 選填：覆寫各 agent 的指令（預設 claude、codex、pi --approve、native-demo）
    pi: [pi, --approve]
  clone: true                    # 沒有 repo 時是否自動 clone
  github-token-file: ~/.agentapi/github.token  # 階段 1：所有 job 共用的 GitHub token；檔案不存在時 job 用主機自己的憑證
  agentapi: ""                   # 選填：job 使用的 agentapi，預設為 launcher 自己（PATH 上的 symlink 優先）
```

### 4.2 repo 探查

- 掃描 `scan` 下深度 2 以內含 `.git` 的目錄，讀 `git remote get-url origin`，正規化成 `owner/name`（同時接受 `git@github.com:` 與 `https://github.com/`，去掉 `.git`）。
- 同一個 repo 出現多次時，以 `workdir` 底下的為主，其餘忽略。
- 每 5 分鐘重掃一次；`POST /jobs` 前會再確認一次。

### 4.3 job 啟動流程（`POST /jobs`）

1. 已經有相同 job key 且還在執行：回 200 與現有 job（冪等）。
2. agent 不在這台主機的 `agents` 內：回 422 `agent_unavailable`。
3. 超過 `max-jobs` 或沒有可用 port：回 409。
4. 找 repo：有就用；沒有且 `clone: true` 時 clone 到 `<workdir>/<owner>/<repo>`；否則回 422 `repo_missing`。clone 與 fetch 都用 §4.5 的 credential helper。
5. `git fetch origin`，然後 `git worktree add <workdir>/<owner>/<repo>.worktrees/<issue> -b agent/issue-<issue> origin/<default_branch>`。分支已存在（之前做過）時改用 `git worktree add <path> agent/issue-<issue>`。
6. git 設定不寫進 repo 或 worktree 的 config，一律以 `GIT_CONFIG_COUNT` 環境變數交給 job（§4.5）。
7. 分配 port，啟動子行程（§4.4）。
8. 等 `/status` 回 `ready`（最多 60 秒），回 201 與 job。

整個啟動不隨 hub 的請求取消：hub 的連線中斷、逾時或 hub 重啟時，launcher 仍會做完，只受自己的時限約束（git 合計最多 10 分鐘，AgentAPI 最多 60 秒，合計 `StartTimeout` 11 分鐘），hub 再從下一次 heartbeat 接回（§5.3）。還在準備（clone、fetch）的 job 在 `GET /jobs` 與 `host.*` 事件裡以 `starting`、沒有 `instance_id` 回報，讓 hub 知道它還在路上。

### 4.4 子行程

```
agentapi server
  -p <port> --type <agent>
  --name "<repo>#<issue>"
  --instance-id "<hostname>~<owner>~<repo>~<issue>"
  --state-db ~/.agentapi/jobs/<key>/state-<run>.db
  --pid-file ~/.agentapi/jobs/<key>/agentapi.pid
  --initial-prompt <§8 的開發 prompt>
  -- <agent 指令>
```

- **環境變數一律清理**：移除所有 `AGENTAPI_*`，以及 `lib/procenv` 已列的 agent session 變數；再設：
  - `AGENTAPI_API_TOKEN=<instance token>`
  - `AGENTAPI_HOST_JOB=<key>`（給 `git-credential`、`gh-token` 辨識 job）
  - `PATH=~/.agentapi/jobs/<key>/bin:$PATH`（放 `gh` shim）
- （e2e 測過：繼承 `AGENTAPI_PID_FILE` 會蓋掉別的 instance 的 PID 檔；`--api-token <值>` 用空白分隔時會被當成 agent 指令。）
- run 從 1 起算，每換一次 agent 加 1（取第一個還沒有 state db 的編號），所以不同 agent 的對話不會混在同一個 db。
- 子行程用 `setsid` 脫離 launcher；launcher 重啟後依 `jobs.json` 記下的 PID 接回（某個參數必須完全等於 instance ID，才算是這個 job 的 AgentAPI；`--pid-file` 只是讓同一個 job 不會跑兩個 server），行程已不在的 job 以同樣參數重啟（`--state-db` 會接回對話，initial prompt 不會重送）。啟動後如果初始的 job metadata 保存失敗，launcher 會停掉新 process 並回報失敗，不讓不受 `jobs.json` 管理的 process 隨成功結果留下。
- launcher 每 5 秒檢查一次。AgentAPI 自己結束（或 60 秒內沒開始服務而被停掉）時，job 進入 `restarting`，依最近 30 分鐘內已重啟的次數等 5 秒、15 秒、45 秒、2 分鐘、5 分鐘後再啟動；重啟 5 次後又結束，job 就標為 `failed`（約 8 分鐘內反覆當掉才會失敗，30 分鐘前的重啟不再計入）。重啟時間與下一次的時間記在 `jobs.json`（`restarts`、`restart_at`），launcher 重啟後照樣等待、累計。launcher 自己重啟時接回已停的 job 不算一次重啟。
- 工作目錄是 worktree。webhook 設定沿用 `config.yaml`，所以 instance 會自動出現在 hub。

### 4.5 GitHub 憑證（自動續期）

job 從不持有長期憑證；需要時經 launcher 向 hub 換一組 installation token。

```
git / gh ──► agentapi host git-credential | gh-token
                 │ 讀 AGENTAPI_HOST_JOB，打 launcher 的 unix socket
                 ▼
             launcher（快取，到期前 5 分鐘才重換）
                 │ POST <hub>/api/github/token（簽章）
                 ▼
             hub：確認 job 存在、在這台主機、repo 相符
                 → 用 App private key 產生只限該 repo 的 installation token
```

- **git**：job 的環境帶 `GIT_CONFIG_COUNT` 設定（不動 repo 的 config）：
  - `credential.https://github.com.helper` 先設為空字串清掉既有的 helper（例如存好的個人 token），再設為 `!agentapi host git-credential --socket <host.sock>`。
  - `url.https://github.com/.insteadOf` 對應 `git@github.com:` 與 `ssh://git@github.com/`，讓 SSH remote 也走 token。
  - 沒有憑證可發時（階段 1：沒有 `github-token-file`）不加這些設定，job 照用主機自己的 SSH key 與 gh 登入。
  - helper 只回應 `protocol=https`、`host=github.com`，且 `path`（有的話）是該 job 的 repo；其餘回空。
- **gh**：`~/.agentapi/jobs/<key>/bin/gh` 是一個 shim：`GH_TOKEN="$(agentapi host gh-token)" exec <真正的 gh> "$@"`。agent 直接用 `gh` 就好。
- **launcher → hub 驗證**：用 `config.yaml` 的 webhook secret 對請求簽章（同 `X-AgentAPI-Signature-256` 格式，時間戳記 5 分鐘內有效），body 帶 `hostname`、`job`。
- **commit 作者**：`git_user` 由 hub 在 `start` 時一併給（`<app-slug>[bot]`、`<app-user-id>+<app-slug>[bot]@users.noreply.github.com`）。launcher 同時設 `user.name`/`user.email` 與 `GIT_AUTHOR_*`、`GIT_COMMITTER_*`，因為 Coder 的 git-config 模組會以 `GIT_AUTHOR_*` 環境變數覆蓋 git config。
- launcher 與 job 子行程之間用 `~/.agentapi/host.sock`（0600，目錄 0700），不經網路。
- **只發給 job 自己的 process**：Linux 上以 `SO_PEERCRED` 取得提出請求的 process，沿 parent 往上必須遇到該 job 的 AgentAPI（或啟動中的 job 由 launcher 自己跑的 git）；另一個 job 的 agent 改了 `AGENTAPI_HOST_JOB` 也拿不到。macOS 不檢查，以 socket 權限限制在同一個使用者。
- git 設定加上 `credential.https://github.com.useHttpPath=true`，helper 才收得到 repo 路徑並只回應該 job 的 repo。
- 啟動中的 job（clone、fetch 期間，還沒寫進 jobs.json）先「預留」：算進 max-jobs、占住 port，它的 git 也能拿 token。
- 向 hub 換 token 時除了 secret 簽章，另帶 launcher 的 host token（`Authorization: Bearer`）。
- hub 沒設定 App、或 hub 連不到時：回錯誤，agent 依 prompt 回報後停下。

### 4.6 launcher API

TCP 介面全部需要 `Authorization: Bearer <host token>`（hub 呼叫）；unix socket 只接受 `git-credential`、`gh-token`。

| 方法 | 路徑 | 說明 |
|---|---|---|
| GET | `/host` | 主機資訊：hostname、版本、repo、agents、容量、job 列表 |
| GET | `/jobs` | job 列表（`JobInfo`：不含 prompt、git 身分與 clone 路徑） |
| POST | `/jobs` | 啟動 job（§4.3） |
| POST | `/jobs/{key}/message` | 轉送到 instance 的 `POST /message` |
| DELETE | `/jobs/{key}` | 停止 job、移除 worktree、刪除本機分支與 state db；`?keep_worktree=true` 保留 worktree 與分支（換 agent 用），`?keep_state=true` 保留 state db |

### 4.7 `host.*` 事件

送到 hub `/hooks`，簽章與 `instance.*` 相同：

- `host.started`、`host.heartbeat`（60 秒）、`host.stopped`
- `data`：`hostname`、`url`（launcher API）、`token`（host token，每個事件都附帶；hub 以它確認主機身分，見 §11）、`agentapi_version`、`repos`、`agents`、`max_jobs`、`jobs: [{key, port, instance_id, state}]`（準備中的 job 沒有 `instance_id`，見 §4.3）

### 4.8 權限與檔案

```
~/.agentapi/
  config.yaml            # webhook 與 host 設定
  host.token             # 0600
  instance.token         # 0600
  host.sock              # 0600
  jobs.json              # launcher 的 job 清單
  jobs/<key>/
    state-<run>.db
    agentapi.pid
    bin/gh               # shim
    agentapi.log         # 子行程輸出
```

### 4.9 安裝

`agentapi host install` 是唯一的安裝路徑，Coder template 與手動安裝都呼叫它：

1. 檢查 agentapi 版本支援 `host`，`config.yaml` 已有 hub 的 webhook 設定（沒有時提示先執行 `agentapi hub join`）。
2. 寫入 `~/.config/systemd/user/agentapi-host.service`（`ExecStart=<agentapi 路徑> host serve`，`Restart=on-failure`，`KillMode=process`：重啟 launcher 時只停 launcher，job 的 AgentAPI 繼續跑，新的 launcher 會接回）。
3. `systemctl --user daemon-reload && systemctl --user enable [--now] agentapi-host`。
4. 檢查 `loginctl show-user $USER -p Linger`，沒開時提示 `sudo loginctl enable-linger $USER`（否則登出後 service 會停）。
5. 沒有 systemd user 時（容器等）：印出錯誤，請改用其他 supervisor 執行 `agentapi host serve`。

舊主機（不是 template 建的）：

```bash
agentapi update --allow-update --restart
agentapi hub join http://<hub>:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
agentapi host install --now
```

hub 畫面的 **+**（加入主機）同時列出這三行。

## 5. hub 變更

### 5.1 儲存

機器與 job 存在 `instances.json` 旁邊的 `dispatch.json`（0600，版本 1），不動 `instances.json`，hub 原有的 instance 儲存邏輯不受影響。內容：hosts、jobs、24 小時內處理過的 `delivery_id`，以及還沒得到結果的啟動（`starts`，以 job key 為鍵：主機、agent、是否換 agent、`delivery_id`、開始時間；§5.3）：

```json
{
  "hosts": [{"hostname": "...", "url": "...", "token": "...", "repos": [], "agents": [], "last_seen": "...", "max_jobs": 4}],
  "jobs":  [{"key": "owner/repo#12", "host": "...", "agent": "claude", "instance_id": "...", "state": "working",
             "issue_url": "...", "pr_url": "", "created_at": "...", "updated_at": "...",
             "runs": [{"agent": "claude", "host": "...", "instance_id": "...", "server_id": "...", "started_at": "...", "ended_at": "...",
                       "end_reason": "replaced | stopped | failed",
                       "cost": {"usd": 1.23, "tokens": {"input": 0, "output": 0, "cache_write": 0, "cache_read": 0},
                                "calls": 42, "tasks": 3, "work_seconds": 1800}}],
             "cost_comment_id": 0,
             "messages": [{"at": "...", "source": "hub | review | comment", "author": "dave", "agent": "claude",
                           "text": "...", "review_state": "changes_requested", "comments": 2}]}]
}
```

`messages` 是送給這個 job 的 agent 的訊息（`send`），由舊到新，最多保留 50 則，每則文字最多 2,000 字。`source` 是來源：`hub`（登入 hub 的瀏覽器，不記 author）、`review`（帶 `review_state` 的 send）、`comment`（`/agent` 留言）。帶 dispatch token 的請求一律不算 `hub`，即使 hub 沒有開登入。舊的 `dispatch.json` 沒有這個欄位，照樣讀得進來。

GitHub App 另外存在 `~/.agentapi/hub/github-app.json`（0600，不併入 `instances.json`）：

```json
{"app_id": 123, "slug": "...", "client_id": "...", "client_secret": "...", "pem": "...",
 "webhook_secret": "...", "bot_user_id": 456, "owner": "k1dav-c",
 "installations": [{"id": 789, "account": "k1dav-c", "repository_selection": "selected"}]}
```

### 5.2 新路由

| 方法 | 路徑 | 驗證 | 說明 |
|---|---|---|---|
| GET | `/api/hosts` | session | 主機、repo、agents |
| DELETE | `/api/hosts/{name}` | session | 移除主機與它綁定的 launcher token（§11）；主機在線（`host_online`）或還有進行中的 job（`host_busy`）時回 409 |
| GET | `/api/jobs` | session | job 列表 |
| POST | `/api/dispatch` | session **或** dispatch token | §7.1 |
| GET | `/api/github` | session | App 設定狀態：未設定、已建立未安裝、已安裝；App 與各安裝的 GitHub 連結（設定頁、安裝頁、大頭貼）與帳號類型（§6.5） |
| GET | `/github/connect?org=` | session | 開始建立 App：一頁自動填好 manifest 的表單（§6.1） |
| GET | `/github/callback` | session + state | manifest 跳轉回來（§6.1） |
| GET | `/github/setup` | session + state | 安裝完成跳轉回來（§6.2） |
| DELETE | `/api/github` | session | 移除 hub 保存的 App（不刪 GitHub 上的 App） |
| GET | `/api/github/repos` | session | App 可存取的 repo，最近 push 或變動的在前（`pushed_at`、`updated_at`） |
| GET | `/api/github/issues?repo=` | session | 列 open issue |
| POST | `/api/github/issues` | session | 建立 issue，可同時 Assign（§5.6） |
| POST | `/api/github/token` | launcher 簽章 | §4.5 |

- hub 的瀏覽器串流（`GET /api/events`）多了 `dispatch` 事件：主機與 job 的完整內容（同 `/api/hosts`、`/api/jobs`），一開始送一次，之後每次變動再送（短時間內的多次變動合併成一次）；主機因漏掉 heartbeat 而離線時，hub 自己的 heartbeat 也會送一次。3.12 以前的頁面不聽這個事件。
- 新增 `--dispatch-token`（`AGENTAPI_HUB_DISPATCH_TOKEN`）：只能呼叫 `/api/dispatch`。
- `/api/github/token`：簽章以 secret 驗證，並以簽章本身去重（delivery ID 不在簽章內）；另須帶該主機的 host token。只發給在該主機上進行中（不含 failed）、用 hub 憑證的 job。
- `host.*` 事件：主機名稱綁定第一次帶來的 host token（§11）；token 不同或沒帶 token 的事件一律拒絕，不論主機是否在線、事件是不是 `host.stopped`。重灌而換了 token 的主機，要先以 `DELETE /api/hosts/{name}`（Issues 頁離線主機的 **×**）移除。
- dispatch.json 在背景寫入（不在鎖內做磁碟 I/O），hub 結束時再寫一次。寫入失敗時保留 pending snapshot，即使沒有新變更也會在一秒後重試，不會被較舊的失敗 snapshot 蓋掉；hub 結束時最後一次寫入仍失敗就記錄錯誤並結束，不無限重試。
- `/github/callback`、`/github/setup` 是瀏覽器跳轉，需要已登入 hub；沒登入時先登入再跳回。

### 5.3 派工規則

`start`：
1. 已有同 key、狀態不是 done 的 job：
   - agent 相同（或 dispatch 沒指定 agent）：回傳它（冪等）。
   - agent 不同：照 §5.7 換 agent；job 還在 `starting` 時回 409 `job_starting`。
2. 有指定 `host`：用它；不符合（沒 repo 又不能 clone、沒有該 agent）時回 422。
3. 沒指定：在線、支援該 agent、job 數低於上限的主機中，先挑有 repo 的，再挑 job 最少的；都沒有 repo 時挑允許 clone 的。
4. 都不行：回 503 `no_host`。
5. 挑好主機後，在同一個鎖內把 job 記為那台主機上的 `starting`，記下這次啟動與 `delivery_id`，就回應（201，job 為 `starting`）。`starting` 的 job 算進主機的 job 數，所以同時進來的多個 start 不會把同一台主機塞過上限。
6. hub 在背景呼叫 launcher 的 `POST /jobs`（換 agent 時先停舊 agent），不用 dispatch 請求的 context，時限為 launcher 的 `StartTimeout` 再加 2 分鐘：
   - 成功：job 改為 `working`，以 App 身分在 issue 留言：主機、agent、hub 上的 instance 連結。
   - launcher 回 409 `capacity`，且主機是 hub 挑的：排除這台，照第 3 點改挑另一台再試；沒有可挑的主機時失敗。
   - launcher 拒絕或失敗：job 標為 `failed`（附 launcher 的錯誤），有 App 時在 issue 留言說明，並忘掉這次的 `delivery_id`，讓 workflow 重跑時能再開工。
   - 沒得到回應（連線中途斷了、逾時、hub 正在關閉）：維持 `starting`，交給主機下一次 heartbeat 判斷（§5.4）。

`send`：找 job → 轉給該主機。job 不存在回 404；主機離線回 503。

`link_pr`：記下 `pr_url`，state 改為 `review`。

`stop`：
1. 轉給 launcher `DELETE /jobs/{key}`。
2. 以 App 身分刪除遠端分支 `agent/issue-<n>`（`DELETE /repos/{o}/{r}/git/refs/heads/agent/issue-<n>`）；422 / 404（已被 GitHub 自動刪除）視為成功。
3. 結束目前的 run，照 §5.8 留言花費彙總。
4. job 標為 `done`，保留紀錄 30 天（花費統計需要）。
- 主機離線時仍刪遠端分支，job 標為 `stopped (host offline)`；主機回來後由下一次 heartbeat 比對，自動清理已不存在的 job。

- 停止還在背景啟動中的 job：hub 立即把它標為 `done`，等 launcher 回應啟動後再呼叫 `DELETE /jobs/{key}` 停掉它；在那之前同一個 issue 的 `start` 回 409 `job_busy`。
- `send` 給還在 `starting` 的 job 回 409 `job_starting`。

所有 dispatch 以 `delivery_id` 去重（保留 24 小時）；背景啟動失敗時會忘掉它。

### 5.4 job 狀態

```
starting ──► working ──► review ──► done
    │           │  ▲        │
    │           ▼  │        ▼
    └────────► failed     stopped
```

- `starting`：hub 已替 job 保留主機上的名額，launcher 正在 clone、fetch 或啟動 AgentAPI（§5.3）。
- `working` / `review` 之間由 instance 的 `task.started`、`task.completed` 與 `link_pr` 推動。
- 主機離線超過 3 次 heartbeat：job 顯示 `host offline`，不改 state。
- 每次 `host.*` 事件，hub 比對主機回報的 job 與自己的紀錄：
  - hub 沒收到結果的啟動（呼叫失敗、hub 重啟）：主機回報了這個 job 且 agent 相符時接回，改為 `working`；回報的是 `failed` 時標 `failed`；回報為準備中（沒有 `instance_id`）時繼續等；啟動已超過 2 次 heartbeat 主機卻沒有這個 job 時標 `failed`。換 agent 時舊 agent 沒停掉的，job 照舊由舊 agent 繼續，並附上「換 agent 沒有完成」的錯誤。
  - 主機回報、但沒有任何 hub 的啟動要求過的 job（hub 不知道、在別台主機上或已 `done`）：在那台主機上停掉，保留 worktree 與 state db（`?keep_worktree=true&keep_state=true`）。準備中與 `failed` 的 job 不動。

### 5.5 畫面

hub 的 **Issues** 頁（標題列的 ◎，或按 `i`）取代總覽的位置；操作說明見 [docs/hub.zh-TW.md 的 Issues](../hub.zh-TW.md#issues)。

1. **看板**：依階段分成 Needs you（失敗、agent 在等你、待 review）、Working、To do、Done。上方是各階段的數量條（To do、In progress、In review、Done，In progress 另外標出等你與失敗的數量）和主機列（在線、job 數與上限、agent）。
2. **範圍**：最近 push 或變動的 20 個 repo（`/api/github/repos` 已依此排序），或單一 repo（可搜尋）；job 一律顯示全部 repo 的。
3. **Assign**：一鍵交給建議的 agent 與主機（hub 的挑法，或上次手動選的 agent）；選單列出每個 agent 能接的主機，接不了的附上原因（離線、已滿、沒有該 agent、沒有 repo 也不能 clone）。
4. **詳細資料**：狀態與主要動作（Retry、Answer、Review、Open chat），Switch agent（§5.7）與 Stop（都要確認），花費統計，run 與訊息的時間軸（§5.1 的 `messages`），分支與 worktree，送訊息給 agent。
5. **即時更新**：主機與 job 來自 `dispatch` 事件；舊 hub 沒有這個事件時每 10 秒讀一次。
6. **徽章與清單**：◎ 上的數字是需要你處理的 issue 數；處理 issue 的 agent 在清單上的那一列標出 issue 的階段和 PR，點了在 Issues 開啟。
7. **New issue**（§5.6），沒有 App 時的 **Connect GitHub**，以及以 `owner/repo#n` 或網址開工；沒有 launcher 時顯示安裝指令。

尚未做：instance 清單依 repo 自動分組。

### 5.6 在 hub 建立 issue

`POST /api/github/issues`：

```json
{"repo": "k1dav-c/foo", "title": "...", "body": "...", "labels": ["bug"],
 "assign": {"agent": "claude", "host": "選填"}}
```

1. 以 App 身分建立 issue（作者會是 `<slug>[bot]`；內文最後加一行「由 <hub 使用者> 在 agentapi hub 建立」）。
2. 有 `assign` 時：
   - 加上 `agent:<type>` label，讓 issue 上看得出由誰處理。
   - 直接在 hub 內執行 `start`，`delivery_id` 為 `hub-issue-<repo>#<n>`。
   - workflow 也會因為 label 觸發 `start`，hub 依 job key 冪等回應，不會開第二個 job。
3. 回傳 issue 與 job。

### 5.7 換 agent 重做

觸發：
- issue 加上另一個 `agent:<type>` label，或
- hub 畫面 **Switch agent**（dispatch `start` 帶不同的 `agent`）。

job 還沒結束時（`working`、`review`、`failed`；還在 `starting` 時回 409 `job_starting`），hub 先把 job 標為 `starting` 並回應，以下在背景進行（§5.3）：
1. launcher 停掉舊的 instance（SIGTERM：AgentAPI 存好狀態再停 agent，所以不另外先 interrupt），**保留 worktree 與分支**（`DELETE /jobs/{key}?keep_worktree=true&keep_state=true`），舊 run 標 `replaced`，記下花費。
2. 以新 agent 在同一個 worktree 啟動 instance，state db 另開新檔（`state-<n>.db`；不同 agent 的對話無法互通），用 §8 的「接手」prompt。
3. job 的 `agent` 改為新的，PR 保持不變。
4. hub 以 App 身分移除 issue 上其他的 `agent:*` label，並留言「改由 <agent> 接手」。

job 已結束（`done`）時：視為新的一輪開發。遠端與本機分支都已刪除，從 default branch 重新開分支，用一般的開發 prompt；`runs` 接在同一個 job 後面，花費一起累計。

### 5.8 花費彙總

- job 每次「重新開一輪」（done 之後再開工）`round` 加 1；花費留言只列本輪的 run，之前幾輪另列一行合計。
- hub 收到 instance 的 `task.completed` / `task.failed` 時，把 `data.cost` 累加到這個 instance 所屬的 run（以 `instance.id` 與 `server_id` 對應 job 和 process generation）；`work_seconds` 取任務的 `duration_seconds` 總和。換 agent 後舊 process 重試送來的延遲事件與目前 run 的 `server_id` 不同，不會改到新 run 的狀態或花費。run 或事件沒有 `server_id`（舊版 launcher 啟動的 run）時不採計，不猜測歸屬；launcher 需與 hub 一起升級，升級後的 launcher 接手 job 時會從 `/status` 取得 `server_id`，下一次回報時由 hub 補上。
- 事件裡的 `instance.id` 與 `server_id` 是送出者自己寫的，所以 hub 還要求自己讀得到這個實例：hub 正從某個網址讀到它的摘要、而且是同一次執行（`server_id`），這個網址的主機也必須和 run 所在主機的 launcher 網址是同一個位址（相同的主機名稱，或解析出共同的位址）。hub 還沒連上實例時送到的事件丟掉，run 標記為可能漏算（`missed`）；在別台主機上讀到的實例，事件一律不採計（§11 第 9 點）。
- 金額是 AgentAPI 換算的 **API 等價價格**；以訂閱方案登入的 agent 實際不會被收這筆錢，留言內要註明。
- job 結束（`stop`）時，hub 以 App 身分在 PR 留言（沒有 PR 時在 issue），一個 job 只留一則，之後重做時更新同一則（`cost_comment_id`）：

  ```
  ### agentapi 花費彙總

  | # | Agent | 主機 | 期間 | 任務數 | 工作時間 | Tokens（in / out / cache） | API 等價花費 |
  |---|---|---|---|---|---|---|---|
  | 1 | Claude Code | pve-dev | 11/08 10:02–10:48 | 3 | 31 分 | 12k / 40k / 1.2M | $1.23 |
  | 2 | Codex | pve-dev | 11/08 11:10–11:30 | 1 | 18 分 | … | $0.40 |
  | | **合計** | | | 4 | 49 分 | | **$1.63** |

  _金額依 API 牌價換算；使用訂閱方案登入時不會實際計費。_
  ```

- hub 漏收事件時（離線、重啟），instance `GET /summary` 的 spend 不足以還原單一 job 的金額，所以花費可能偏低；表格註明「hub 未收到部分事件」。

## 6. GitHub App 設定（manifest flow）

在 hub 上按一次 **Connect GitHub** 完成，不必手動貼 key。所有跳轉都經過瀏覽器，所以 hub 在內網也能完成；只有 §6.1 第 4 步是 hub 主動連到 `api.github.com`。

### 6.1 建立 App

1. 使用者選擇擁有者：個人帳號或 org（例如 `k1dav-c`）。
2. 瀏覽器開 `/github/connect`（建在組織下時加 `?org=<組織>`）：hub 產生一次性 `state`（10 分鐘內有效），回傳一頁表單，按下後 POST 到：
   - 個人：`https://github.com/settings/apps/new?state=<state>`
   - org：`https://github.com/organizations/<org>/settings/apps/new?state=<state>`
3. manifest：

   ```json
   {
     "name": "agentapi-<hub hostname>",
     "url": "<hub 的網址>",
     "redirect_url": "<hub 的網址>/github/callback",
     "setup_url": "<hub 的網址>/github/setup",
     "setup_on_update": true,
     "public": false,
     "hook_attributes": {"url": "<hub 的網址>/github/unused", "active": false},
     "default_permissions": {
       "contents": "write",
       "pull_requests": "write",
       "issues": "write",
       "metadata": "read"
     }
   }
   ```

   hub 的網址取自瀏覽器目前的 origin，所以 `http://192.168.9.210:3290` 也能用（GitHub 只把使用者導回這個網址，不會自己連）。webhook 設為停用，事件由 runner 處理。

4. 使用者在 GitHub 按 **Create GitHub App** 後，GitHub 把瀏覽器導到 `/github/callback?code=...&state=...`。hub 驗證 `state`，一小時內呼叫 `POST https://api.github.com/app-manifests/{code}/conversions`，取得 `id`、`slug`、`client_id`、`client_secret`、`pem`、`webhook_secret`，存入 `github-app.json`；再查 `GET /users/<slug>[bot]` 取得 bot 的 user id。
5. hub 把瀏覽器導向 `https://github.com/apps/<slug>/installations/new?state=<新的 state>`，讓使用者選要安裝的 repo。
6. App 的名稱預設為 `agentapi-hub-<6 位隨機十六進位>`（GitHub 上 App 名稱不能重複），可在 GitHub 的表單上修改。

### 6.2 安裝

- 安裝或修改安裝後，GitHub 把瀏覽器導到 `setup_url`（`/github/setup?installation_id=...&setup_action=install|update`）。
- hub 用 App JWT 確認這個 installation 屬於自己的 App，記下 installation；導回 Hub settings。
- 之後 hub 需要某個 repo 的 token 時：用 App JWT 查 `GET /repos/{o}/{r}/installation` 取得 installation id，再 `POST /app/installations/{id}/access_tokens`，指定 `repositories: [<repo>]` 與 §6.3 的權限。token 依 repo 快取到到期前 5 分鐘。

### 6.3 token 權限

| 用途 | 權限 |
|---|---|
| 給 job（git、gh） | `contents: write`、`pull_requests: write`、`issues: write` |
| hub 自己（列 issue、留言、刪分支） | 同上 |

### 6.4 手動設定（備援）

`agentapi hub serve --github-app-file <path>` 讀入相同格式的 JSON，給無法用瀏覽器完成 manifest flow 的情況（例如 GitHub 帳號設定不允許）。

### 6.5 之後的調整：Hub settings 的 GitHub 分頁

接上 App 之後，Hub settings 的 **GitHub** 分頁（Issues 頁上 App 的名稱、repo 選單底部的 **GitHub settings…** 也會開到這裡）列出 App、擁有者，以及每個安裝的帳號、`all` 或選了哪些 repo。所有改動都在 GitHub 上做，分頁只提供連結，頁面重新顯示時再讀一次 `/api/github`：

- **Choose repositories**：`https://github.com/apps/<slug>/installations/new`，加裝到其他帳號或改選 repo。各帳號的 **Change** 直接到該安裝：個人 `https://github.com/settings/installations/<id>`，組織 `https://github.com/organizations/<org>/settings/installations/<id>`。改完 GitHub 依 `setup_on_update` 導回 `/github/setup`，hub 更新那個 installation（§6.2）。
- **App settings on GitHub**：個人 `https://github.com/settings/apps/<slug>`，組織 `https://github.com/organizations/<org>/settings/apps/<slug>`。hub 從 manifest 轉換（`owner.type`）與 installation（`account.type`）記下帳號類型；3.14 以前建立的 App 沒有記錄，要等擁有者自己的 installation 再經過一次 `/github/setup` 才知道，在那之前連到 App 的公開頁 `https://github.com/apps/<slug>`。
- **Reconnect**：選個人帳號或組織後到 `/github/connect`（§6.1），**不先刪除**目前的 App。`/github/callback` 拿到新 App 才以 `saveApp` 原子地取代 `github-app.json` 與記憶體中的 App，並清掉舊 App 快取的 token；在 GitHub 上取消，舊 App 照常運作。新 App 沒有 installation，裝好之前 job 拿不到 token。舊 App 留在 GitHub，由使用者自己刪除。
- **Disconnect**：確認後 `DELETE /api/github`，hub 刪掉 `github-app.json` 與快取的 token。執行中的 job 拿不到新 token，手上的 token 到期（≤ 1 小時）後推送與開 PR 會失敗；GitHub 上的 App 與安裝不受影響。

## 7. 資料格式

### 7.1 `POST /api/dispatch`

```json
{
  "action": "start | send | link_pr | stop",
  "delivery_id": "github-run-<run_id>-<attempt>",
  "repo": "k1dav-c/foo",
  "issue": 12,
  "agent": "claude | codex | pi | 空字串（主機預設）",
  "host": "選填",
  "message": "send 用：review 或留言內容（原文，由 hub 套用 §8 的格式）",
  "author": "send 用：留言或 review 的人",
  "review_state": "send 用：commented | changes_requested",
  "comments": [{"path": "...", "line": 10, "body": "..."}],
  "dispatch_mode": "queue | steer",
  "replace": "選填，預設 true：start 遇到 agent 不同的進行中 job 時是否換 agent（false 則回 409 job_active）",
  "pr_url": "link_pr 用"
}
```

- 不再帶 `github_token`：token 一律由 hub 產生。
- `default_branch`、`issue_url` 由 hub 以 App 查詢，不信任 dispatch 的值。

回應：`{"job": {...}, "created": true|false}`。`start` 在 job 於主機上進入 `starting` 時就回應（§5.3），不等 clone 與 agent 啟動；之後的結果經 hub 的串流、`GET /api/jobs`，以及有 App 時 issue 上的留言得知。

## 8. Prompt

prompt 範本由 hub 套用，在 `start` 時交給 launcher。

開發用（`start`，作為 `--initial-prompt`）：

```
你負責處理 GitHub issue {{repo}}#{{issue}}（{{issue_url}}）。
工作目錄是這個 issue 專用的 worktree，分支 agent/issue-{{issue}}。
git 與 gh 已設定好 GitHub 憑證，直接使用即可。

1. 用 gh issue view {{issue}} --comments 讀完整內容。需求不清楚時，在 issue 留言提問後停下。
2. 實作，跑專案的測試和 lint。
3. commit，push 到 agent/issue-{{issue}}。
4. 用 gh pr create 開 PR，內文寫 "Closes #{{issue}}"，列出改了什麼、怎麼測的。
5. 之後收到的訊息是 review 意見：在同一個分支修改、push，不要另開 PR。
push 或 gh 因權限失敗時，回報錯誤並停下，不要嘗試其他憑證。
```

接手用（換 agent，§5.7）：

```
你接手 GitHub issue {{repo}}#{{issue}}（{{issue_url}}），之前由 {{previous_agent}} 處理。
工作目錄與分支 agent/issue-{{issue}} 保留了之前的進度{{#if pr_url}}，PR 是 {{pr_url}}{{/if}}。
git 與 gh 已設定好 GitHub 憑證，直接使用即可。

1. 先讀 issue（gh issue view {{issue}} --comments）、git log origin/{{default_branch}}..HEAD、git status{{#if pr_url}}，以及 PR 的 review 與留言（gh pr view --comments）{{/if}}，了解目前進度。
2. 從目前的狀態繼續完成，不要重寫已經可用的部分。
3. 跑測試和 lint，commit 並 push 到同一個分支{{#if pr_url}}；不要另開 PR{{else}}，然後用 gh pr create 開 PR，內文寫 "Closes #{{issue}}"{{/if}}。
push 或 gh 因權限失敗時，回報錯誤並停下，不要嘗試其他憑證。
```

review 轉送（`send`）：

```
PR {{pr_url}} 有新的 review（{{author}}，{{review_state}}）。
以下是使用者提供的內容：
<<<
{{message}}
{{#each comments}}- {{path}}:{{line}} {{body}}
{{/each}}
>>>
請處理後 push 到同一個分支，完成後在 PR 留言摘要你改了什麼。
```

## 9. GitHub Actions 端

### 9.1 reusable workflow（`k1dav-c/.github/.github/workflows/agent-dispatch.yml`）

檔案與架設說明在 agentapi repo 的 `examples/issue-dispatch/`（`agent-dispatch.yml`、`caller.yml`、`README.md`），放到中央 repo 後使用。

| 事件 | 條件 | action |
|---|---|---|
| `issues: labeled` | label 為 `agent` 或 `agent:<type>`，`sender` 有 write 權限 | `start`（agent 取自 label） |
| `issue_comment: created` | 在 PR 上、開頭 `/agent`、`author_association` ∈ OWNER/MEMBER/COLLABORATOR | `send` |
| `pull_request_review: submitted` | head 分支 `agent/issue-*`、`head.repo.full_name == github.repository`、reviewer 有 write 權限、state ≠ `approved` | `send` |
| `pull_request: opened` | head 分支 `agent/issue-*` | `link_pr` |
| `pull_request: closed` | head 分支 `agent/issue-*` | `stop` |

- issue 編號從分支名 `agent/issue-<n>` 取得。
- 一次只處理事件裡的那個 label：加上不同的 `agent:<type>` 會觸發換 agent（§5.7）；hub 會移除其他 `agent:*` label，移除 label 不觸發任何動作。
- `/agent` 留言在 issue 或它的 PR 上都可以；PR 上的留言以 PR 的 head 分支找回 issue。`/agent` 後面須是空白或結尾；不是 agent 分支的 PR 上的 `/agent` 會被忽略。
- 權限：`author_association` 只做初步過濾（組織的 MEMBER 不一定有 write）；job 再以 `GET /repos/{repo}/collaborators/{user}/permission` 查加 label、留言、review 的人，只接受 write（含 maintain）或 admin，查不到就忽略。
- review 只有逐行留言、本文是空的也會轉送（逐行留言另以 API 讀取）；兩者都沒有時不轉送。
- `delivery_id` 是 `github-<repo>-<run_id>`：重跑同一個 run 只算一次（失敗的不算，所以重跑可以重試）。
- 事件內容以 `jq` 從 `$GITHUB_EVENT_PATH` 讀取，不把 `${{ }}` 插進 shell。

job 規則：
- `runs-on: [self-hosted, agent-dispatch]`
- 不 checkout code，不需要任何 GitHub secret。
- 事件欄位一律用 `env:` 傳，以 `jq -n --arg` 組 JSON 後 `curl`，不用 `${{ }}` 插進 shell。
- `HUB_URL`、`HUB_DISPATCH_TOKEN` 從 runner 主機的環境變數讀。
- 失敗時以 `GITHUB_TOKEN` 在 issue 或 PR 留言錯誤原因（成功時的留言由 hub 以 App 身分處理）。

各 repo 的 caller：

```yaml
on:
  issues: { types: [labeled] }
  issue_comment: { types: [created] }
  pull_request_review: { types: [submitted] }
  pull_request: { types: [opened, closed] }
permissions:
  issues: write
  pull-requests: write
jobs:
  dispatch:
    uses: k1dav-c/.github/.github/workflows/agent-dispatch.yml@main
```

### 9.2 runner

- 專用 Linux 使用者（例如 `gh-runner`），不是 agent 的使用者。
- 只需能連到 hub；不需 git、gh、agent 的登入。
- runner group 只開放給 private repo。

## 10. Coder template（`k1dav-c/coder-templates`）

在 `modules/ai-agents` 加入，改完以 `scripts/sync-modules.sh` 同步到 `pve`、`remote-host`。

- 參數 `enable_agent_host`（order 406，預設 false，mutable）。
- 變數 `hub_url`、`hub_secret`（sensitive）、`hub_instance_token`（sensitive）；也可經 `extra_env` 注入 `AGENTAPI_HUB_URL`、`AGENTAPI_HUB_SECRET`、`AGENTAPI_HUB_INSTANCE_TOKEN`。
- `coder_script`（`run_on_start`），在 agentapi binary 安裝完成後執行：
  1. `agentapi` 版本不支援 `host` 時，執行 `agentapi update`（不論 `agentapi_auto_update` 的設定）。
  2. `config.yaml` 沒有 hub 設定時，執行 `agentapi hub join "$HUB_URL/hooks" --secret ... --public-url 'http://{ip}:{port}'`。
  3. 把 `hub_instance_token` 寫入 `~/.agentapi/instance.token`。
  4. `agentapi host install --now`。
- 同時修正既有問題：
  - 移除 Gemini（AgentAPI 3 不支援）。
  - 既有 agent 的 `start_script` 改用 `--state-db`（舊的 `AGENTAPI_STATE_FILE` 已無作用）。
  - 既有 agent 的 `start_script` 經 `AGENTAPI_API_TOKEN` 帶入 `hub_instance_token`。
  - binary 改放 `~/.local/bin/agentapi`，`/usr/local/bin` 留 symlink，讓 hub 能更新。
- workspace 停止時不停止 job：VM 關機後 job 隨之中斷，開機後 launcher 依 `jobs.json` 重啟，`--state-db` 接回對話。
- 文件提醒：要讓 job 持續進行，workspace 的 autostop 要關閉或調長。

## 11. 安全性

1. **公開 repo 不使用 self-hosted runner。** fork PR 能改 workflow 並在內網執行任意程式。
2. **Issue 內容是不可信的 prompt。** `start` 只由 label 觸發（加 label 需要 write 權限）或在 hub 手動 Assign；留言與 review 只接受有 write 權限的人。轉送內容一律包在分隔區塊內。
3. **token 分級：**

   | token | 誰持有 | 能做什麼 |
   |---|---|---|
   | hub token | 你的瀏覽器 | 完整的 hub 權限 |
   | dispatch token | runner 主機 | 只能 `/api/dispatch` |
   | host token | hub | hub → launcher |
   | webhook secret | 主機 | 簽章 `host.*`、`instance.*` 事件與換 token 請求 |
   | instance token | hub、launcher | 呼叫 job instance |
   | installation token | job（經 launcher 快取） | 只限一個 repo、一小時 |
   | App private key | 只有 hub | 產生 installation token |

4. **換 token 的檢查**：hub 只對「這台主機上、狀態不是 done 的 job，且 repo 相符」發 token。webhook secret 外洩時，冒用已登記的主機名稱會被拒絕（第 7 點），外洩者只能以新的名稱加入，拿到 hub 派給它的 job 的 repo token；輪替 secret 後所有主機需重新 `hub join`。
5. `github-app.json` 0600；hub 的 log 不輸出 token 與 key。
6. launcher 與 job instance 都必須有 token；launcher 建議 `bind` 內網位址。

7. **主機身分（trust on first use）**：webhook secret 是所有加入的主機共用的，所以它不代表某一台主機。hub 第一次收到某個主機名稱的 `host.*` 事件時，記下事件裡的 host token，之後這個名稱只接受同一個 token：token 不同或沒帶 token 的事件一律拒絕（不論主機是否在線，`host.stopped` 也一樣），`/api/github/token` 也只認這個 token。否則持有 secret 的機器可以先送 `host.stopped` 再冒名送 heartbeat，取得那台主機上 job 的 GitHub token，並讓 hub 把派工送到自己那裡。主機重灌或 `host.token` 遺失時，先停掉它的 job，等它離線後以 `DELETE /api/hosts/{name}`（Issues 頁離線主機的 **×**）移除，新的 launcher 下一次 heartbeat 就會重新加入。
8. **同一使用者的信任邊界**：主機上的 job 都以同一個使用者執行，agent 讀得到 `~/.agentapi/` 裡的 secret 與 host token。token 只發給 job 自己的 process（Linux），但同一台主機上的 agent 仍可讀其他檔案；需要更強的隔離時，以不同使用者或不同主機跑 job。
9. **任務事件綁定主機**：`task.*` 事件同樣只用共用的 webhook secret 簽章，事件裡的實例 ID 是送出者自己宣告的。hub 只在它正從 job 所在主機的位址讀到那個實例（摘要裡是同一個 `server_id`）時才採計（§5.8），launcher 網址由第 7 點綁定的 host token 背書，所以持有 secret 的機器在自己的位址宣告別台主機的實例 ID，改不了那個 job 的狀態與花費。剩下的信任邊界仍是 secret：實例 ID 和 `server_id` 都在摘要裡，持有 secret、又讀得到摘要（所有 job 共用 instance token）的機器，照樣能以那個實例的名義送事件；要根除需要每台主機各一把金鑰。
10. **launcher 網址（避免 SSRF）**：`host.*` 事件裡的 `url` 由送出者決定，hub 會以那台 launcher 的 token 呼叫它（start、send、stop，以及 reconcile 時自動停掉沒人要求的 job）。hub 只接受 http 或 https、不含帳號密碼、有主機的網址，並在每次連線時檢查實際連到的位址（名稱解析後；hub 不經 proxy 直接連線，也不跟隨重新導向）：拒絕 loopback、link-local（`169.254.0.0/16`、`fe80::/10`，含雲端 metadata `169.254.169.254`）與 `0.0.0.0`／`::`，內網位址照常允許。hub 本機上的 launcher 以 hub 的內網位址加入時，回報的就是那個位址，不受影響；以 `localhost` 加入而回報 `127.0.0.1` 的，要以 `agentapi hub serve --allow-loopback-launchers` 啟動 hub。剩下的邊界：持有 secret 的機器仍能讓 hub 對內網其他機器的 `/jobs` 送請求（帶的是它自己的 launcher token）。

## 12. 失敗處理

| 情況 | 處理 |
|---|---|
| 主機離線 | dispatch 回 503，workflow 失敗並留言；可在 Actions 重跑 |
| clone 與 agent 啟動很久 | hub 在 job 進入 `starting` 時就回應 workflow（不受 curl 的 60 秒限制）；launcher 的 git 最多 10 分鐘、AgentAPI 最多 60 秒 |
| 背景啟動失敗 | job 標 `failed`，有 App 時在 issue 留言；`delivery_id` 不保留，可在 Actions 重跑 |
| hub 呼叫 launcher 沒得到回應、或 hub 中途重啟 | launcher 照樣做完；hub 由下一次 heartbeat 接回，主機沒有這個 job 時標 `failed`（§5.4） |
| 主機上有 hub 不知道的 job | 停掉，保留 worktree 與 state db（§5.4） |
| 同時開工、或 hub 記的容量與主機不同 | 挑主機與記下 `starting` 在同一個鎖內；launcher 回 409 `capacity` 時改挑另一台 |
| launcher 換了 host token | hub 拒絕它的事件（§11）；移除主機後重新加入 |
| launcher 回報 loopback、link-local 等 hub 不呼叫的網址 | hub 拒絕它的事件（400，§11 第 10 點），主機不會上線；以 hub 的內網位址重新 `hub join`，或以 `--allow-loopback-launchers` 啟動 hub |
| 重複事件（workflow 重跑） | `delivery_id` 去重；`start` 本身冪等 |
| port 或容量不足 | 409，留言說明 |
| repo 不在且不允許 clone | 422 `repo_missing` |
| 該主機沒有指定的 agent | 422 `agent_unavailable`（自動挑主機時會跳過它） |
| hub 未設定 GitHub App | 照常派工，但不經 GitHub：不留言、不刪分支，job 用主機自己的憑證（`credentials` 為空）；`/api/github/*` 回 412 `github_not_connected` |
| App 未安裝在該 repo | `start` 回 412 `github_not_installed`，留言附安裝連結 |
| 換 token 失敗 | git / gh 回錯誤；agent 回報後停下；hub 畫面在 job 上標示 |
| job instance 當掉 | launcher 依 PID 檔偵測，等 5 秒到 5 分鐘不等後以同樣參數重啟；30 分鐘內重啟 5 次後又當掉就標 `failed`（§4.4） |
| 用量限制 / API 暫時錯誤 | 沿用 AgentAPI 既有的 limit stop 與 retry，佇列中的訊息不會遺失 |

## 13. 相容性

- hub、launcher、job instance 使用相同版本；舊 hub 收到 `host.*` 事件時忽略。
- `instances.json` 新增欄位不升版本；`github-app.json` 是新檔案。
- 不改動 `agentapi server` 的旗標與行為。

## 14. 分階段與驗收

### 階段 1：launcher（已實作，2026-11-08）

- [x] `agentapi host serve / install / uninstall / status` 可用；launcher 重啟後接回 job。
- [x] 環境清理：在一個 agent 的 shell 內啟動 launcher，job 不會改到任何既有的 PID 檔。
- [x] 沒帶 token 打 launcher 或 job instance 回 401。
- [x] 對同一個 job 重複 `POST /jobs` 回同一個 job。
- [x] e2e（真的 Claude，在 `k1dav-c/coder-templates` 的 clone 上）：start → commit（作者為 `git_user`）→ send review → 再 commit → 換 agent 保留 worktree（run 2）→ stop 後 worktree 與本機分支移除。
- [ ] `agentapi host install` 只有單元測試，還沒在真的 systemd user 上跑過。
- [ ] 經 `github-token-file` 的 push 與 `gh pr create` 還沒在真的 GitHub 上跑過（helper 與 shim 有單元測試）。
- 尚未做（留到階段 2）：`host.*` 事件、從 hub 換 token、Coder template。

### 階段 2：hub 派工與 GitHub App（已實作，2026-11-08）

程式在 `lib/dispatch`（派工、prompt、花費、GitHub 設定流程）、`lib/github`（App JWT、installation token、REST），hub 只掛路由並轉送 `host.*`、`task.*` 事件。

- [x] `host.*` 註冊主機（每次都帶 host token）；主機回報少了 job 時 hub 標 failed，離線時 stop 的 job 等主機回來再清。
- [x] `/api/dispatch` 四個 action、派工規則、`delivery_id` 去重；dispatch token 打其他路由回 401。
- [x] 換 agent（§5.7）、`stop` 刪除遠端分支並在 PR 留言花費彙總（§5.8，重開一輪時清掉舊留言 ID）、在 hub 建立 issue（§5.6）。
- [x] `/api/github/token` 只對「這台主機上進行中、用 hub 憑證的 job」發 token；同一個 delivery 不回第二次。launcher 與 hub 都快取 token 到到期前 5 分鐘。
- [x] job 的 AgentAPI 讀 launcher 的設定檔（`--config`），並加送 `task.*` 事件（`AGENTAPI_WEBHOOK_EVENTS`），hub 才收得到花費。
- [x] 測試：假的 launcher 與 GitHub API（含驗證 App JWT 簽章）跑完整流程；e2e：真的 hub + launcher + Claude，經 dispatch token 開工、去重、review 轉送、花費回寫、stop。
- [ ] **Connect GitHub** 還沒在真的 GitHub 上走過（manifest、安裝、token、留言、刪分支都只有假 API 的測試）。

### 階段 3：GitHub Actions 與畫面（已實作，2026-11-08）

- [x] reusable workflow 與 caller、runner 架設說明（`examples/issue-dispatch/`）；actionlint 通過；以假的 hub 與 GitHub API 跑過五種事件與 hub 拒絕時的留言，issue 標題與留言裡的 `$(…)` 不會被執行。
- [x] hub 的 Issues 頁（3.12 起取代對話框，見 §5.5）。原本的對話框（標題列的 ◎ 按鈕）：機器、GitHub App 的狀態與 Connect、repo 的 open issue 與各自的 job（狀態、agent、主機、PR、花費、Open chat）、Assign（選 agent 與主機）、送訊息、換 agent、Stop、New issue（可直接 Assign）；沒有 GitHub App 時以 `owner/repo#n` 或 issue 網址開工。以 headless Chromium 在真的 hub + launcher 上點過開工、送訊息、Open chat、Stop。
- [x] 機器的 job 數與挑主機時的空位，算入 hub 剛開、主機下一次 heartbeat 還沒回報的 job。
- [ ] 在真的 GitHub 上：加 `agent:claude` label 後開出 bot 身分的 PR、review 與 `/agent` 轉送、PR 關閉後刪分支與花費留言、加 `agent:codex` 換 agent（需要 GitHub App 與內網 runner）。

### 階段 4：Coder template

- [ ] `enable_agent_host` 開啟後，新 workspace 自動 join hub 並啟動 launcher。
- [ ] 舊主機以 §4.9 的三行指令加入，行為相同。
- [ ] 一併修正：移除 Gemini、`--state-db`、instance token、binary 位置。

## 15. 待決定

目前沒有。

## 16. 實作備註

給實作這份 spec 的人（以 v3.9.0 的程式碼為準）：

- **hub 路由**：`lib/hub/hub.go` 的 `routes()`，瀏覽器用的 API 在 `requireSession` 群組內；`/hooks` 在 `checkHost` 與 cross-origin 保護之外。`/api/dispatch` 的 dispatch token 驗證要另寫一個 middleware，不要放寬 `signedIn`（`lib/hub/session.go`）。
- **hub 儲存**：`lib/hub/store.go`（`Record`）與 `lib/hub/persist.go`；新增欄位比照 favorites 的相容做法（見 CHANGELOG v3.5.0 與 v3.7.4：版本維持 1，舊 hub 讀得到，寫回時會丟掉新欄位）。
- **webhook 簽章**：沿用 `lib/instanceapi` 的 `Sign` 與標頭常數；`host.*` 事件與 `/api/github/token` 的簽章驗證共用 `lib/hub/webhook.go` 的去重與時效檢查。
- **設定檔**：`lib/userconfig`（`~/.agentapi/config.yaml`），`host:` 區段由這裡讀寫。
- **子行程環境**：以 `lib/procenv` 為基礎，額外移除所有 `AGENTAPI_*`。
- **登記目錄**：job instance 照常寫入 `lib/registry`（`agentapi list` 看得到）。
- **schema**：hub 的 wire type 改動後執行 `make gen` 與 `cd chat && bun run gen:api`，commit `lib/hub/hub-schema.json` 與 `chat/src/lib/hub-schema.ts`。
- **commit**：Conventional Commits（`feat:`、`fix:`），CI 依此發版；使用者看得到的變更寫進 `CHANGELOG.md`，文件同時更新英文與 `.zh-TW.md`。
- **2026-11-08 手動 e2e 的發現**（已寫進 §4.4）：`--api-token <值>` 用空白分隔會被當成 agent 指令；從 agent 的 shell 啟動會繼承 `AGENTAPI_PID_FILE` 而蓋掉別的 instance 的 PID 檔；hub 只會帶一組 instance token，所以 job instance 必須共用它。
