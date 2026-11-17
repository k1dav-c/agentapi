# Issue 派工：目前的流程

Issue 派工把一個 issue 的開發、review 與收尾串在同一個 job：hub 決定交給誰，launcher 在主機上執行，
內網 runner 把 GitHub 事件送回 hub。

1. **開工：**label、hub 或 API 觸發派工，launcher 建立 worktree 並啟動 agent。
2. **修改：**review 與留言回到同一個 agent；換 agent 時沿用分支與 worktree。
3. **收尾：**PR 關閉後停止 job，清理 worktree 與分支。

> 對應 PR [k1dav-c/agentapi#81](https://github.com/k1dav-c/agentapi/pull/81)（`feat/issue-dispatch`）的階段 1～3，設計細節見 [issue-dispatch.zh-TW.md](issue-dispatch.zh-TW.md)。
>
> Issues 頁的截圖使用真實 hub 與固定測試資料（三台主機，以及 `acme/web`、`acme/api`、`acme/infra` 的 issue 與 job）。`dispatch-chat.png` 使用加入 hub 的真實 server，以 `native-demo` 模擬 Claude Code，呈現 `acme/web#12` 的 review prompt；`dispatch-github-connect.png` 則呈現 hub 尚未連接 App 時的設定頁。各張截圖的來源見 [docs/screenshots/README.md](../screenshots/README.md)。

本文記錄階段 1～3 的流程與驗證進度，下方待辦保留當時的驗證狀態。目前的操作方式請見 [hub 使用指南](../hub.zh-TW.md)。

<a id="1-架構"></a>

## 1. hub 派工、launcher 執行，runner 傳回 GitHub 事件

```mermaid
flowchart LR
  subgraph GH["GitHub"]
    issue["Issue<br/>label agent:claude"]
    pr["PR<br/>review、/agent 留言"]
    app["hub 的 GitHub App<br/>（bot 身分）"]
  end

  subgraph LAN["內網"]
    runner["self-hosted runner<br/>agent-dispatch.yml"]
    subgraph HUB["hub（agentapi hub serve）"]
      dispatch["dispatch<br/>/api/dispatch"]
      ghsvc["GitHub App 金鑰<br/>/api/github/token"]
      ui["Issues 頁"]
      list["instance 清單、chat 代理"]
    end
    subgraph HOST["主機（每台一個）"]
      launcher["launcher<br/>agentapi host serve"]
      job1["job：acme/widget#12<br/>worktree + Tendyard + agent"]
      job2["job：acme/api#7<br/>worktree + Tendyard + agent"]
    end
  end

  you(["你"])

  issue -- "事件" --> runner
  pr -- "事件" --> runner
  runner -- "主動拉 job，再 POST<br/>（dispatch token）" --> dispatch
  you -- "瀏覽器" --> ui
  ui --> dispatch
  you -- "直接對話" --> list
  dispatch -- "start / message / stop<br/>（host token）" --> launcher
  launcher --> job1
  launcher --> job2
  launcher -- "host.* heartbeat<br/>（secret 簽章）" --> HUB
  job1 -- "instance.*、task.*<br/>（狀態、花費）" --> HUB
  job1 -- "要 token" --> launcher
  launcher -- "換 repo token<br/>（簽章 + host token）" --> ghsvc
  ghsvc -- "installation token" --> app
  job1 -- "git push、gh pr create<br/>（bot 身分）" --> GH
  dispatch -- "留言、刪分支、花費彙總" --> app
```

GitHub 不需要連進內網：事件由 runner 主動去拉，GitHub App 的設定也只靠瀏覽器跳轉。issue 交給哪台主機、哪個 instance，只記在 hub 的 `dispatch.json`。App 的 private key 只在 hub，主機拿到的都是限一個 repo、一小時失效的 token。

<a id="2-開工issue-加上-label"></a>

## 2. issue 加上 label 後，由 launcher 啟動專用 job

```mermaid
sequenceDiagram
  autonumber
  actor U as 你
  participant GH as GitHub
  participant R as runner
  participant H as hub
  participant L as launcher
  participant J as job（Tendyard + agent）

  U->>GH: issue 加 label agent:claude
  GH-->>R: issues.labeled
  R->>GH: 查加 label 的人的權限（write / admin 才繼續）
  R->>H: POST /api/dispatch {start, repo, issue, agent}
  H->>GH: 讀 issue、default branch（App token）
  H->>H: 挑主機：在線、有該 agent、有空位、優先有 repo<br/>同一個鎖內記下 job：starting
  H-->>R: 201 created（job：starting）
  H->>L: POST /jobs（背景；prompt、bot 的 git 身分、credentials=hub）
  L->>L: 預留位置與 port
  L->>GH: clone / fetch（token 經 hub 取得）
  L->>L: worktree：repo.worktrees/12，分支 agent/issue-12
  L->>J: 啟動 agentapi server（--initial-prompt）
  J-->>L: /status ready
  L-->>H: JobInfo（instance ID）
  Note over H: job 狀態 → working（沒收到回應時，由下一次 heartbeat 接回）
  H->>GH: issue 留言「Claude Code 在 pve 上開始處理」
  J->>J: 讀 issue → 實作 → 測試
  J->>GH: push agent/issue-12、gh pr create（bot 身分）
  GH-->>R: pull_request.opened
  R->>H: link_pr
  Note over H: job 狀態 → review
```

也可以不經 GitHub 的 label，在 hub 的 Issues 頁開工（標題列的 ◎ 或 `i`）。看板依階段排列，需要你處理的在最前面：失敗的 #9、等你核准的 #7、待 review 的 #12；To do 裡還沒交辦的 issue 按 **Assign**，就交給按鈕上標示的 agent 和主機。

![Issues 頁](../screenshots/dispatch-issues.png)

Assign 旁邊的箭頭列出每個 agent 能接的主機，接不了的附上原因：

![交辦選單](../screenshots/dispatch-assign.png)

New issue 會在 GitHub 開一個 issue，打開 Assign to 就直接交給 agent：

![New issue](../screenshots/dispatch-new-issue.png)

<a id="3-review-來回"></a>

## 3. Review 與留言會回到同一個 agent

```mermaid
sequenceDiagram
  autonumber
  actor U as 你
  participant GH as GitHub
  participant R as runner
  participant H as hub
  participant L as launcher
  participant J as job

  alt 在 GitHub 上 review
    U->>GH: Request changes（或 /agent 留言）
    GH-->>R: pull_request_review / issue_comment
    R->>GH: 查 reviewer 權限、讀逐行留言
    R->>H: POST /api/dispatch {send, message, comments}
    H->>L: POST /jobs/{key}/message（隨機圍欄包住你的內容）
    L->>J: POST /message
  else 在 hub 上直接對話
    U->>H: Issues → Open chat 或送訊息，或從清單打開 instance
    H->>J: 代理 chat（hub 帶 instance token）
  end
  J->>GH: 修改、push 到同一個分支、在 PR 留言摘要
  U->>GH: Approve、merge
```

在 Issues 點開 issue，時間軸會列出每次 run，以及送給 agent 的每則訊息：從 hub 送的，以及從 GitHub 轉來的 review 和 `/agent` 留言（hub 每個 job 保留最近 50 則）。

![issue 的詳細資料](../screenshots/dispatch-review.png)

在 hub 按 Open chat，看到的就是 job 的對話。GitHub 上的 review 轉進來時，hub 用隨機的 BEGIN／END 標記包住 reviewer 寫的內容，告訴 agent 那是使用者提供的資料、不是指示：

![Open chat](../screenshots/dispatch-chat.png)

<a id="4-結束pr-關閉"></a>

## 4. PR 關閉後停止 job 並清理分支

```mermaid
sequenceDiagram
  participant GH as GitHub
  participant R as runner
  participant H as hub
  participant L as launcher

  GH-->>R: pull_request.closed
  R->>H: POST /api/dispatch {stop}
  H->>L: DELETE /jobs/{key}
  L->>L: 停實例、移除 worktree 與本機分支
  H->>GH: 刪除遠端分支 agent/issue-12
  H->>GH: PR 留言花費彙總（本輪每個 run 一列）
  Note over H: job 狀態 → done（保留 30 天）
```

在 Issues 的詳細資料按 Stop 會先確認，因為 worktree 和分支（本機與 GitHub）都會刪掉：

![Stop 確認](../screenshots/dispatch-confirm-stop.png)

<a id="5-換-agent"></a>

## 5. 換 agent 時沿用同一個分支與 worktree

在 issue 加上另一個 `agent:<type>` label，或在 Issues 的詳細資料按 Switch agent 選 agent（會先確認）。

```mermaid
sequenceDiagram
  participant H as hub
  participant L as launcher
  participant J1 as Claude
  participant J2 as Codex

  H->>H: 先讀 issue（失敗就什麼都不動）
  Note over H: job 狀態 → starting，先回應，以下在背景進行
  H->>L: DELETE /jobs/{key}?keep_worktree&keep_state
  L->>J1: 停止（保留 worktree、分支、對話紀錄）
  H->>L: POST /jobs {agent: codex, 接手 prompt}
  L->>J2: 在同一個 worktree 啟動（新的 run、新的 state db）
  J2->>J2: 讀 git log、PR 留言，接著做
  Note over H: 中途失敗 → job 標為 failed 並附原因
```

![Switch 確認](../screenshots/dispatch-confirm-switch.png)

<a id="6-token-怎麼給到-job"></a>

## 6. job 經 launcher 向 hub 換取 repo token

```mermaid
flowchart TB
  git["job 裡的 git / gh"] -- "credential helper / gh shim<br/>（unix socket host.sock）" --> l["launcher"]
  l -- "Linux：SO_PEERCRED，沿 parent 往上<br/>必須是這個 job 的 process" --> check{"是這個 job 的？"}
  check -- "否" --> deny["403"]
  check -- "是" --> cache{"快取的 token<br/>還有 5 分鐘以上？"}
  cache -- "有" --> give["回傳 token"]
  cache -- "沒有" --> hub["hub /api/github/token"]
  hub -- "驗證：secret 簽章、簽章去重、<br/>host token、job 在這台且進行中" --> mint["App JWT → 只限該 repo 的<br/>installation token（1 小時）"]
  mint --> give
```

沒有 GitHub App 時照樣能派工，只是 job 用主機自己的憑證（SSH key、`gh auth login`），hub 也不留言、不刪分支。接上 App 之前，也可以在 launcher 設 `github-token-file`，讓所有 job 共用一個 token 先跑起來。

還沒接上 App 時，Issues 頁會提示 Connect GitHub，此時要貼 issue 的網址或 `owner/repo#12` 開工：

![還沒接上 GitHub](../screenshots/dispatch-no-github.png)

按 Connect GitHub 會到 hub 的 `/github/connect`，再按一次就到 GitHub 建立 App。在 GitHub 上建立與安裝的畫面沒有截圖，這部分還沒用真的 GitHub 走過。

![/github/connect](../screenshots/dispatch-github-connect.png)

接上之後，改安裝的 repo、換一個 App 或中斷連接，都在 Hub settings 的 **GitHub** 分頁（Issues 頁上點 App 的名稱也會開到這裡）。Reconnect 不會先中斷：新 App 從 GitHub 回來才取代舊的，中途取消一切照舊（規格 §6.5）。

![Hub settings 的 GitHub 分頁](../screenshots/hub-settings-github.png)

<a id="7-job-狀態"></a>

## 7. Job 狀態跟隨派工與 agent 的執行結果

```mermaid
stateDiagram-v2
  [*] --> starting: start（hub 先回應，launcher 在背景啟動）
  starting --> working: agent 起來
  starting --> failed: launcher 拒絕／沒啟動／換 agent 失敗
  starting --> done: stop
  working --> review: PR 開了（link_pr）／任務結束且有 PR
  review --> working: send（review 意見）
  working --> starting: 換 agent
  working --> failed: launcher 回報失敗／主機上已沒有這個 job
  failed --> starting: Retry（同一或另一個 agent）
  working --> done: stop
  review --> done: stop（PR 關閉）
  failed --> done: stop
  working --> stopped: stop 時主機離線
  stopped --> done: 主機回來後清掉
  done --> working: 再開工（新的一輪）
```

Tendyard 退出後，launcher 依序等 5 秒、15 秒、45 秒、2 分鐘、5 分鐘再重啟；30 分鐘內重啟 5 次後又退出，就不再重啟，job 標為 failed，並附上 log 的位置（可複製）。Retry 用原本的 agent 重來，也可以按 Switch agent 換一個：

![失敗的 job](../screenshots/dispatch-failed.png)

<a id="8-你目前能怎麼用"></a>

## 8. 可從 label、hub 或 API 交辦 issue

| 想做的事 | 在 GitHub | 在 hub |
|---|---|---|
| 交給 agent | issue 加 `agent` 或 `agent:claude`／`codex`／`pi` | Issues（◎ 或 `i`）→ 卡片上的 Assign，或旁邊的箭頭選 agent 與主機 |
| 開新 issue 並交辦 | — | Issues → New issue（打開 Assign to） |
| 回饋修改 | PR review（非 approve），或 `/agent <內容>` | issue 的詳細資料 → 最下面的輸入框，或 Open chat 直接對話 |
| 換 agent | 加另一個 `agent:<type>` label | 詳細資料 → Switch agent → 確認 |
| 重試失敗的 job | 把 label 拿掉再加回去 | 卡片或詳細資料的 Retry |
| 結束 | 關閉或 merge PR | 詳細資料 → Stop → 確認 |
| 看花費 | PR 關閉時的花費彙總留言 | 卡片上的金額，詳細資料裡每次 run 的金額 |
| 看哪些需要你 | — | ◎ 上的數字；清單上處理 issue 的 agent 標出 issue 與 PR |

<a id="9-還沒完成"></a>

## 9. 這些端對端驗證與部署工作仍列為待辦

- [ ] 用真實的 GitHub 走一次：Connect GitHub、安裝 App、內網 runner、bot 身分開 PR。
- [ ] 階段 4：Coder template 自動啟動 launcher，並修掉 template 的既有問題（Gemini、`--state-db`、instance token、binary 位置）。
- [x] 畫面：instance 清單上標出 job 和 PR（點了在 Issues 開啟），每次 run 的明細（Issues 詳細資料的時間軸）。
- [ ] 畫面：instance 清單依 repo 自動分組。
- [ ] `agentapi host install` 在真的 systemd 上跑一次。
