# Issue 派工：架設

設定 hub、launcher、GitHub App 與內網 runner，即可讓 agent 處理 GitHub issue，並把 PR 的 review
交回同一個 agent。依相依順序完成四項設定：

1. **hub 接收派工：**設定專用 dispatch token，供 runner 呼叫。
2. **launcher 執行工作：**每台主機先加入 hub，再啟動 launcher。
3. **GitHub App 提供身分：**授權 repo，讓 job 能 push 與開 PR。
4. **runner 傳回事件：**在內網執行 workflow，把 issue 與 PR 事件送到 hub。

從空白主機開始、每一步附上檢查方式的完整流程，見[架設 Tendyard](../../docs/setup.zh-TW.md)；這份說明
著重派工本身。設計細節見 [docs/specs/issue-dispatch.zh-TW.md](../../docs/specs/issue-dispatch.zh-TW.md)。

```
GitHub ──(runner 主動拉 job)──► 內網 runner ──POST /api/dispatch──► hub ──► 各主機的 launcher ──► 每個 issue 一個實例
```

<a id="1-hub"></a>

## 1. hub 用 dispatch token 接收 runner 的派工

在 hub 啟動參數加上 dispatch token（只能呼叫 `/api/dispatch`，給 runner 用）：

```bash
agentapi hub serve --token "$HUB_TOKEN" --secret "$HUB_SECRET" --instance-token "$INSTANCE_TOKEN" \
  --dispatch-token "$HUB_DISPATCH_TOKEN"
```

hub 把機器與 job 存在 `~/.agentapi/hub/dispatch.json`，GitHub App 存在 `github-app.json`（都是 0600）。

`--instance-token` 只給手動開的 Tendyard 用；launcher 開的 job 用各主機自己產生的 token（`~/.agentapi/instance.token`），launcher 會在事件裡交給 hub，不必複製到各台主機。

<a id="2-每台主機的-launcher"></a>

## 2. 每台主機加入 hub 後，以 launcher 啟動 issue job

主機先加入 hub（`agentapi hub join`），再把 launcher 裝成 systemd user service：

```bash
agentapi hub join http://<hub>:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
agentapi host install --now
agentapi host status
```

選填的設定放在 `~/.agentapi/config.yaml` 的 `host:` 區段：

```yaml
host:
  workdir: ~/work          # clone 與 worktree 的位置
  scan: [~/work, ~/src]    # 找既有 clone 的目錄
  agents: [claude, codex]  # 預設依已安裝的 CLI 偵測
  agent: claude            # label 只有 agent 時用的 agent
  max-jobs: 4
```

`agentapi host status` 列出這台主機的 repo、agent 與 job；hub 的 `/api/hosts` 應該看得到它。

只有要接 issue 的主機才需要 launcher；只想在 hub 上監看的主機，`agentapi hub join` 就夠了。沒有
systemd 的主機（容器、Coder 工作區）上 `agentapi host install` 會失敗，改用
`nohup agentapi host serve >~/.agentapi/host.log 2>&1 &`，或從 Coder 範本的 `coder_script` 啟動，見
[`agentapi host`](../../docs/cli.zh-TW.md#agentapi-host)。

<a id="3-github-app"></a>

## 3. GitHub App 提供 repo 存取權與 bot 身分

登入 hub 後，用瀏覽器開：

```
http://<hub>:3290/github/connect?org=<組織>     # 個人帳號就不加 ?org=
```

按下按鈕到 GitHub 建立 App，接著選要安裝的 repository。整個過程只有瀏覽器在 GitHub 與 hub 之間跳轉，hub 在內網也可以。之後：

- agent 以 `<app>[bot]` 的身分 commit、push、開 PR，你可以用自己的帳號 approve。
- 主機上的 job 需要 token 時向 hub 換一組只限該 repo、一小時失效的 token；主機不保存長期憑證。
- hub 以 App 身分在 issue 留言進度、在 PR 留言花費彙總、PR 關閉時刪除分支。

沒有 App 時派工照常運作，但 job 用主機自己的憑證（SSH key、`gh auth login`），也不會留言。

<a id="4-內網-runner-與-workflow"></a>

## 4. 內網 runner 透過 workflow 把事件送回 hub

<a id="runner"></a>

### runner 必須連得到 hub

找一台連得到 hub 的機器，用一個**專用的使用者**（不要用跑 agent 的使用者）裝 GitHub Actions runner，
label 設為 `agent-dispatch`，並在 runner 目錄的 `.env` 寫入 `HUB_URL` 與 `HUB_DISPATCH_TOKEN`。
runner group、fork PR 的設定、服務安裝，以及確認 runner 連得到 hub 的指令，見
[架設 Tendyard 的 runner 一節](../../docs/setup.zh-TW.md#runner)。

注意：能用這個 runner 的 repo，它的 workflow 都讀得到 `HUB_DISPATCH_TOKEN`。這組 token 只能呼叫
`/api/dispatch`，但仍請只把 runner 開放給你信任的 repo。

runner 只需要 `bash`、`curl`、`jq`，不需要 git、gh 或任何 agent 的登入。

<a id="workflow"></a>

### workflow 把 issue 與 PR 事件轉成派工請求

1. 把 [`agent-dispatch.yml`](agent-dispatch.yml) 放到中央 repo，例如 `<組織>/.github` 的
   `.github/workflows/agent-dispatch.yml`。私有的中央 repo 要在它的 Settings → Actions → General →
   Access 允許組織內的 repo 使用。公開 repo 無法呼叫私有中央 repo 的 workflow，也不應開放使用這個 runner。
2. 每個要派工的 repo 加上 [`caller.yml`](caller.yml)（例如 `.github/workflows/agent.yml`），
   把裡面的 `k1dav-c/.github` 換成中央 repo。

<a id="使用"></a>

### 加上 label 即可開工，review 會回到同一個 agent

| 在 GitHub 上 | 結果 |
|---|---|
| issue 加上 `agent` 或 `agent:claude`、`agent:codex`、`agent:pi` label | 開始開發；換另一個 `agent:*` label 會換 agent，在同一個分支接手 |
| PR review（不是 approve） | 轉給同一個 agent 修改 |
| 在 issue 或 PR 留言 `/agent <內容>` | 轉給同一個 agent |
| PR 關閉或 merge | 停止，刪除分支，留言花費彙總 |

job 會向 GitHub 查觸發者對這個 repo 的權限，只有 write（含 maintain）或 admin 的人加的 label、留言和
review 才會派工或轉送；查不到時一律忽略。組織的 MEMBER 不一定有 write 權限，所以不以 author_association
判斷。issue 範本自動帶上的 `agent` label 也會檢查開 issue 的人。fork 來的 PR 一律不處理。
留言要以 `/agent` 加空白開頭（`/agents …` 不算）；在不是 agent 分支的 PR 上留言 `/agent` 一律忽略。
也可以在 hub 的畫面直接打開 job 的 instance 對話。

開工時，hub 一挑好主機、把 job 記為 `starting` 就回應 workflow，主機接著在背景 clone 或 fetch
repo、啟動 agent，所以 workflow 不必等到 clone 完成。之後的結果由 hub 在 issue 上留言（需要 GitHub
App）：開始處理，或沒能開始與原因。背景失敗的開工不會記下 delivery ID，在 Actions 重跑即可再開工。
workflow 本身失敗並留言「hub 沒有接受這次派工」的，只有 hub 當下就拒絕的情況：沒有在線的主機、主機
沒有這個 agent、App 沒裝在這個 repo 等。
