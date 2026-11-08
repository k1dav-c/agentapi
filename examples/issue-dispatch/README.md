# Issue 派工：架設

把 GitHub issue 交給內網主機上的 agent 開發，PR 的 review 再轉給同一個 agent。設計見
[docs/specs/issue-dispatch.zh-TW.md](../../docs/specs/issue-dispatch.zh-TW.md)。

```
GitHub ──(runner 主動拉 job)──► 內網 runner ──POST /api/dispatch──► hub ──► 各主機的 launcher ──► 每個 issue 一個 AgentAPI
```

需要四樣東西：hub、每台主機的 launcher、hub 的 GitHub App、內網的 self-hosted runner 加 workflow。

## 1. hub

在 hub 啟動參數加上 dispatch token（只能呼叫 `/api/dispatch`，給 runner 用）：

```bash
agentapi hub serve --token "$HUB_TOKEN" --secret "$HUB_SECRET" --instance-token "$INSTANCE_TOKEN" \
  --dispatch-token "$HUB_DISPATCH_TOKEN"
```

hub 把機器與 job 存在 `~/.agentapi/hub/dispatch.json`，GitHub App 存在 `github-app.json`（都是 0600）。

## 2. 每台主機的 launcher

主機先加入 hub（`agentapi hub join`），再把 launcher 裝成 systemd user service：

```bash
agentapi hub join http://<hub>:3290/hooks --secret "$HUB_SECRET" --public-url 'http://{ip}:{port}'
echo "$INSTANCE_TOKEN" > ~/.agentapi/instance.token   # 必須等於 hub 的 --instance-token
chmod 600 ~/.agentapi/instance.token
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

## 3. GitHub App

登入 hub 後，用瀏覽器開：

```
http://<hub>:3290/github/connect?org=<組織>     # 個人帳號就不加 ?org=
```

按下按鈕到 GitHub 建立 App，接著選要安裝的 repository。整個過程只有瀏覽器在 GitHub 與 hub 之間跳轉，hub 在內網也可以。之後：

- agent 以 `<app>[bot]` 的身分 commit、push、開 PR，你可以用自己的帳號 approve。
- 主機上的 job 需要 token 時向 hub 換一組只限該 repo、一小時失效的 token；主機不保存長期憑證。
- hub 以 App 身分在 issue 留言進度、在 PR 留言花費彙總、PR 關閉時刪除分支。

沒有 App 時派工照常運作，但 job 用主機自己的憑證（SSH key、`gh auth login`），也不會留言。

## 4. 內網 runner 與 workflow

### runner

找一台連得到 hub 的機器，用一個**專用的使用者**（不要用跑 agent 的使用者）裝 GitHub Actions runner：

1. **組織**：Settings → Actions → Runner groups 建一個 group，只開放給要派工的 **private** repo
   （不要勾 Allow public repositories：公開 repo 的 fork PR 能在 runner 上執行任意程式）。
   Runner group 只有組織有；**個人帳號**的 repo 要在每個 repo 各自加 runner（repo 的 Settings → Actions → Runners）。
   也確認 Settings → Actions → General 的「Run workflows from fork pull requests」是關的。
2. Settings → Actions → Runners → New self-hosted runner，照畫面下載與設定；
   `./config.sh` 時加上 `--labels agent-dispatch`（組織另加 `--runnergroup <上一步的 group>`）。
3. 在 runner 目錄的 `.env` 寫入 hub 的位址與 dispatch token（runner 會把它們帶進 job 的環境）：

   ```
   HUB_URL=http://<hub>:3290
   HUB_DISPATCH_TOKEN=<hub 的 --dispatch-token>
   ```

   `chmod 600 .env`。之後改了 `.env` 要重啟 runner 服務（`sudo ./svc.sh stop && sudo ./svc.sh start`）才會生效。
4. `sudo ./svc.sh install <使用者> && sudo ./svc.sh start`。

注意：能用這個 runner 的 repo，它的 workflow 都讀得到 `HUB_DISPATCH_TOKEN`。這組 token 只能呼叫
`/api/dispatch`，但仍請只把 runner 開放給你信任的 repo。

runner 只需要 `bash`、`curl`、`jq`，不需要 git、gh 或任何 agent 的登入。

### workflow

1. 把 [`agent-dispatch.yml`](agent-dispatch.yml) 放到中央 repo，例如 `<組織>/.github` 的
   `.github/workflows/agent-dispatch.yml`。私有的中央 repo 要在它的 Settings → Actions → General →
   Access 允許組織內的 repo 使用；公開的 repo 呼叫不到私有中央 repo 的 workflow（反正公開 repo 也不該用這個 runner）。
2. 每個要派工的 repo 加上 [`caller.yml`](caller.yml)（例如 `.github/workflows/agent.yml`），
   把裡面的 `k1dav-c/.github` 換成中央 repo。

### 使用

| 在 GitHub 上 | 結果 |
|---|---|
| issue 加上 `agent` 或 `agent:claude`、`agent:codex`、`agent:pi` label | 開始開發；換另一個 `agent:*` label 會換 agent，在同一個分支接手 |
| PR review（不是 approve） | 轉給同一個 agent 修改 |
| 在 issue 或 PR 留言 `/agent <內容>` | 轉給同一個 agent |
| PR 關閉或 merge | 停止，刪除分支，留言花費彙總 |

job 會向 GitHub 查觸發者對這個 repo 的權限，只有 write（含 maintain）或 admin 的人加的 label、留言和
review 才會派工或轉送；查不到時一律忽略。組織的 MEMBER 不一定有 write 權限，所以不以 author_association
判斷。issue 範本自動帶上的 `agent` label 也會檢查開 issue 的人。fork 來的 PR 一律不處理。
留言要以 `/agent` 加空白開頭（`/agents …` 不算）；在不是 agent 開的 PR 上留言 `/agent` 會被忽略。
也可以在 hub 的畫面直接打開 job 的 instance 對話。
