# 架設 Tendyard

[English](setup.md) | [繁體中文](setup.zh-TW.md)

這份指南從空白的主機開始，一路做到 agent 把 GitHub issue 變成 pull request。請依序進行：每一步都需要
前面的步驟，結尾也都附上檢查方式。

- **一台主機、一個 agent：**只需要[安裝執行檔](#安裝執行檔)，接著執行 `agentapi server -- claude`，
  打開 `http://localhost:3284/chat`，和 [README](../README.zh-TW.md#快速開始) 相同。
- **多台主機集中在一頁：**完成 hub 與工作主機的步驟，做到[讓每台工作主機加入](#讓每台工作主機加入)。
- **把 GitHub issue 交給 agent：**完成所有步驟。

## 目錄

- [四種角色分工](#角色)
- [開始前先確認這些條件](#開始之前)
- [憑證只需要產生一次](#憑證)
- [把執行檔裝在能自行更新的位置](#安裝執行檔)
- [把 hub 執行成服務](#執行-hub)
- [讓每台工作主機加入](#讓每台工作主機加入)
- [連接 hub 的 GitHub App](#github-app)
- [架設轉送 GitHub 事件的 runner](#runner)
- [在每個 repo 加上 workflow 與 label](#workflow-與-label)
- [派出第一個 issue](#第一個-issue)
- [保持最新版本](#更新)
- [架設時常見的狀況](#架設問題)

<a id="角色"></a>

## 四種角色分工

| 角色 | 執行 | 需要 |
| --- | --- | --- |
| hub 主機 | `agentapi hub serve`，常駐 | 瀏覽器、工作主機與 runner 都連得到 |
| 工作主機 | agent：`agentapi server` 實例；要接 issue 的再加上 launcher `agentapi host serve` | 已登入的 agent CLI；接 issue 需要 `git` 與 `gh` |
| runner 主機 | 自架的 GitHub Actions runner，label 為 `agent-dispatch` | `bash`、`curl`、`jq`；連得到 hub |
| GitHub | hub 的 GitHub App、一個中央 workflow，以及每個 repo 的 caller workflow | 能建立 App 與 runner 的組織或帳號 |

一台主機可以兼任多個角色：hub 和工作主機可以是同一台，runner 和 hub 也可以。不過 runner 要用和
agent 不同的使用者執行，避免 workflow 碰到 agent 的登入憑證。

<a id="開始之前"></a>

## 開始前先確認這些條件

**網路。**每一列是左邊那台主動建立的連線，請在左邊的主機上用 `curl` 確認：

| 從 | 到 | Port | 用途 |
| --- | --- | --- | --- |
| 你的瀏覽器或手機 | hub | 3290 | hub 的頁面 |
| 工作主機 | hub | 3290（`/hooks`） | 實例與 launcher 送出簽章過的事件 |
| hub | 工作主機 | 各實例的 port（3284…）、launcher 的 3291、job 的 4100–4199 | hub 讀取摘要、代理聊天介面、啟動 job |
| runner | hub | 3290（`/api/dispatch`） | workflow 的派工請求 |
| runner | github.com | 443 | runner 向 GitHub 領取 job；GitHub 不會主動連進來 |

hub 不需要對外的網址：GitHub 從不呼叫它，GitHub App 的設定流程也只經過你的瀏覽器。請使用主機之間
能直接連到的位址，例如區網或 VPN 位址。需要先登入的網址（例如 Coder 工作區的 port 網址）不能當
`/hooks` 用：事件會被轉址到登入頁，送不到 hub。

**每台工作主機上，**以執行 agent 的使用者確認：

- 要用的 agent CLI（`claude`、`codex`、`pi`）已安裝並登入。在終端機各執行一次確認。沒登入時，聊天
  介面會出現登入卡片，派來的 job 也會停在那裡等。
- 已安裝 `git` 與 `gh`。agent 用 `gh issue view` 讀 issue，用 `gh pr create` 開 pull request。有
  GitHub App 時，launcher 會給兩者一組只限該 repo 的 token；沒有時則使用這台主機自己的憑證
  （`gh auth login`、SSH key）。
- 登出後服務仍會繼續執行。`agentapi host install` 與使用者層級的 hub 服務，需要在登出後仍存在的
  systemd user instance：`sudo loginctl enable-linger <user>`。沒有 systemd 的容器（例如 Coder
  工作區）要用其他方式啟動 launcher（[`agentapi host`](cli.zh-TW.md#agentapi-host)）。

<a id="憑證"></a>

## 憑證只需要產生一次

每個值只產生一次，存在密碼管理器裡。各自放在不同的地方：

```bash
openssl rand -hex 32   # HUB_TOKEN：登入 hub 頁面用
openssl rand -hex 32   # HUB_SECRET：實例與 launcher 用來簽署事件
openssl rand -hex 32   # HUB_DISPATCH_TOKEN：runner 呼叫 POST /api/dispatch 用
```

| 值 | 放在 | 能做什麼 |
| --- | --- | --- |
| `HUB_TOKEN` | hub（`AGENTAPI_HUB_TOKEN`）、你的瀏覽器 | hub 頁面能做的所有事 |
| `HUB_SECRET` | hub（`AGENTAPI_HUB_SECRET`）、每台工作主機的 `agentapi hub join` | 向 hub 回報實例與主機 |
| `HUB_DISPATCH_TOKEN` | hub（`AGENTAPI_HUB_DISPATCH_TOKEN`）、runner 的 `.env` | 只能呼叫 `POST /api/dispatch` |
| 實例 API token（選用） | hub（`AGENTAPI_HUB_INSTANCE_TOKEN`）、你手動以 `--api-token` 啟動的實例 | 使用那些實例的 API |

launcher 啟動的 job 不用實例 token：每個 launcher 會自己產生一組（`~/.agentapi/instance.token`）並交給
hub。各個值能存取什麼，見 [Token 讓受信任主機共用存取權](hub.zh-TW.md#token-與安全性)。

<a id="安裝執行檔"></a>

## 把執行檔裝在能自行更新的位置

每台主機都以實際執行它的使用者，把 `agentapi` 裝到 `~/.local/bin`：

```bash
OS=$(uname -s | tr "[:upper:]" "[:lower:]")
ARCH=$(uname -m | sed "s/x86_64/amd64/;s/aarch64/arm64/")
mkdir -p "$HOME/.local/bin"
curl -fsSL "https://github.com/k1dav-c/agentapi/releases/latest/download/agentapi-${OS}-${ARCH}" \
  -o "$HOME/.local/bin/agentapi"
chmod +x "$HOME/.local/bin/agentapi"
agentapi --version
```

`agentapi update` 與 hub 發起的更新，會直接替換執行檔所在位置的檔案。放在使用者無法寫入的目錄
（例如 root 擁有的 `/usr/local/bin`）時，沒有 `sudo` 就無法更新，hub 也會把那台主機標成需要一次性
設定。如果有程式非得從 `/usr/local/bin` 找到它，就建立連結：
`sudo ln -sf "$HOME/.local/bin/agentapi" /usr/local/bin/agentapi`。已經用另一種方式裝好的主機，修正
方法見[從 hub 更新](hub.zh-TW.md#從-hub-更新)。

**檢查：**`agentapi --version` 印出你下載的版本。

<a id="執行-hub"></a>

## 把 hub 執行成服務

在 hub 主機上，把這些值寫進只有你能讀的檔案：

```bash
mkdir -p ~/.config
cat > ~/.config/agentapi-hub.env <<'EOF'
AGENTAPI_HUB_TOKEN=<HUB_TOKEN>
AGENTAPI_HUB_SECRET=<HUB_SECRET>
AGENTAPI_HUB_DISPATCH_TOKEN=<HUB_DISPATCH_TOKEN>
EOF
chmod 600 ~/.config/agentapi-hub.env
```

再把它執行成 systemd user service：

```ini
# ~/.config/systemd/user/agentapi-hub.service
[Unit]
Description=Tendyard hub

[Service]
EnvironmentFile=%h/.config/agentapi-hub.env
ExecStart=%h/.local/bin/agentapi hub serve --port 3290
Restart=on-failure

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now agentapi-hub
```

沒有啟用 lingering 時（見[開始前先確認這些條件](#開始之前)），改用 system unit：同一份檔案放在
`/etc/systemd/system/`，加上 `User=<你>`，`EnvironmentFile` 改成絕對路徑，`WantedBy=multi-user.target`，
並以 `sudo systemctl` 管理。

加上 `--bind <位址>` 可以只聽某個介面，例如 VPN 的位址。hub 的檔案都在 `~/.agentapi/hub/`：實例、派工
的主機與 job、GitHub App，以及模型 provider。

**檢查：**

```bash
systemctl --user status agentapi-hub
journalctl --user -u agentapi-hub | grep 'agentapi hub join'   # 每個位址各一行 join 指令
```

打開 `http://<hub>:3290/`，用 `HUB_TOKEN` 登入。空的 hub 會顯示如何連接第一批 agent。

<a id="讓每台工作主機加入"></a>

## 讓每台工作主機加入

在每台工作主機上執行一次。secret 放在環境變數裡，不要留在 shell 歷史紀錄：

```bash
read -rs AGENTAPI_HUB_SECRET && export AGENTAPI_HUB_SECRET
agentapi hub join http://<hub>:3290/hooks --public-url 'http://{ip}:{port}' --allow-update
```

- `join` 會先送一個簽章過的測試事件，hub 不接受就停下。
- 它把 hub 寫進 `~/.agentapi/config.yaml`，這個使用者的每個 `agentapi server` 啟動時都會讀。正在執行的
  實例會立即加入。
- `{ip}` 會換成這台主機通往 hub 的位址，位址變了也會跟著更新。
- `--allow-update` 讓 hub 可以更新這台主機的實例；不加的話就自己手動更新。

要讓每個實例的對話在重啟與更新後保留下來，在 `~/.agentapi/config.yaml` 加上這一行：

```yaml
state-db: ~/.agentapi/state/{port}.db
```

如果這台主機要接 issue，也啟動它的 launcher：

```bash
agentapi host install --now   # systemd user service；容器的做法見 agentapi host
agentapi host status
```

launcher 聽 3291 port，hub 從 `http://{ip}:3291` 連它。設定檔的 `host` 區段可以設定 clone repo 的位置、
提供哪些 agent，以及同時執行幾個 job（[`agentapi host`](cli.zh-TW.md#agentapi-host)）。

**檢查：**

```bash
agentapi hub status   # hub，以及每個執行中的實例回報到哪裡
agentapi host status  # launcher：它的 hub、agent、GitHub 憑證、repo 與 job
```

hub 的清單會出現這台主機的實例，Issues 頁也會列出這台主機與它的 agent。如果 launcher 就在 hub 那台
主機上、以 `127.0.0.1` 加入，hub 啟動時要加上 `--allow-loopback-launchers`。

<a id="github-app"></a>

## 連接 hub 的 GitHub App

登入 hub 後，打開：

```text
http://<hub>:3290/github/connect?org=<組織>   # 個人帳號就不加 ?org=
```

在 GitHub 上確認建立，再選要安裝的 repo。整個過程只有你的瀏覽器在 GitHub 與 hub 之間往返，所以 hub
可以留在內網。App 要求的權限是 contents、pull requests、issues、workflows（寫入）與 metadata（讀取）。
有了它，agent 以 `<app>[bot]` 的身分 push 並開 pull request，hub 也會在 issue 與 pull request 上留言。

**檢查：**hub 設定的 GitHub 分頁顯示這個 App 與它的安裝處（[GitHub 設定](hub.zh-TW.md#github-設定)），
Issues 頁也列出這些 repo 的 open issue。

<a id="runner"></a>

## 架設轉送 GitHub 事件的 runner

GitHub 不會呼叫 hub。由一台連得到 hub 的自架 runner 領取 workflow 的 job，再把每個事件 POST 給 hub。
在 runner 主機上：

1. **建立專用的使用者，**不要用執行 agent 的那個：`sudo useradd -m gh-runner`。安裝 `curl` 與 `jq`。
2. **限制誰能使用它。**
   - 組織：Settings → Actions → Runner groups，建一個只開放給要派工的 private repo 的 group。
     **Allow public repositories** 保持關閉：公開 repo 的 fork 發來的 pull request 可能在 runner 上執行
     任何東西。
   - 個人帳號沒有 runner group：在每個 repo 各自加 runner（Settings → Actions → Runners）。
   - Settings → Actions → General 的 **Run workflows from fork pull requests** 保持關閉。
3. **註冊 runner。**在 Settings → Actions → Runners → New self-hosted runner，以那個使用者照 GitHub 的
   下載與 `./config.sh` 步驟進行，並加上 `--labels agent-dispatch`（組織另加 `--runnergroup <group>`）。
4. **給它 hub 的位址與 dispatch token，**寫在 runner 目錄的 `.env`，runner 會帶進每個 job：

   ```bash
   cat > .env <<'EOF'
   HUB_URL=http://<hub>:3290
   HUB_DISPATCH_TOKEN=<HUB_DISPATCH_TOKEN>
   EOF
   chmod 600 .env
   ```

5. **執行成服務：**`sudo ./svc.sh install gh-runner && sudo ./svc.sh start`。它在啟動時讀 `.env`：
   改了 `.env` 之後，要執行 `sudo ./svc.sh stop && sudo ./svc.sh start`。

**檢查：**在 runner 主機上以它的使用者執行。回 `400` 表示 hub 接受了 token、拒絕了空的請求；`401`
表示 token 不對；沒有回應表示連不到 hub：

```bash
set -a; . ./.env; set +a
curl -s -o /dev/null -w '%{http_code}\n' -X POST "$HUB_URL/api/dispatch" \
  -H "Authorization: Bearer $HUB_DISPATCH_TOKEN" -H 'Content-Type: application/json' -d '{}'
```

GitHub 的 Settings → Actions → Runners 會顯示這台 runner 為 Idle。能使用這台 runner 的每個 workflow
都讀得到 `HUB_DISPATCH_TOKEN`。這組 token 只能派工，但仍請只開放給信任的 repo。

<a id="workflow-與-label"></a>

## 在每個 repo 加上 workflow 與 label

1. 把 [`agent-dispatch.yml`](../examples/issue-dispatch/agent-dispatch.yml) 放進一個中央 repo，例如
   `<組織>/.github`，路徑為 `.github/workflows/agent-dispatch.yml`。中央 repo 是 private 的話，要在它的
   Settings → Actions → General → Access 允許其他 repo 使用。
2. 每個要派工的 repo 加上 [`caller.yml`](../examples/issue-dispatch/caller.yml)，路徑為
   `.github/workflows/agent.yml`，並把裡面的 `k1dav-c/.github` 換成你的中央 repo。
3. 建立 workflow 會回應的 label：

   ```bash
   for label in agent agent:claude agent:codex agent:pi; do
     gh label create "$label" --repo <owner>/<repo> --color 5319e7 \
       --description 'Hand this issue to an agent' --force
   done
   ```

   工作主機沒有的 agent 就不用建。只加 `agent` 時，使用 launcher 的預設 agent。

每個 label、review 與留言會觸發什麼，見 [workflow 的事件](../examples/issue-dispatch/README.md#使用)。
只有對 repo 有寫入權限的人才算數。

<a id="第一個-issue"></a>

## 派出第一個 issue

在加了 workflow 的 repo 開一個小 issue，加上 `agent` label，接著依序確認：

1. **Actions** 分頁在 runner 上執行 `agent` workflow。失敗並留言「Tendyard hub 沒有接受這次派工」，表示
   hub 拒絕了這次派工：沒有在線的主機、沒有主機提供那個 agent，或 App 沒有裝在這個 repo。
2. hub 的 Issues 頁先顯示 Starting，接著在它挑選的主機上開始工作。hub 會在 issue 上留言說 agent 已開始。
3. 從 job 的詳細資料打開它的聊天介面，追蹤進度或回覆請求。agent 會 push `agent/issue-<n>` 並開
   pull request。
4. review 這個 pull request。要求修改的 review，或 `/agent …` 留言，會回到同一個 agent。merge 或關閉
   pull request 會停止 job、刪除分支，並留言花費彙總。

也可以不透過 runner 與 workflow，直接在 hub 開工：Issues 頁的 **Assign**（[Issues](hub.zh-TW.md#issues)）。
這樣的話，review 不會自動回到 agent。

<a id="更新"></a>

## 保持最新版本

- **hub：**在 hub 主機執行 `agentapi update`，再重啟服務（`systemctl --user restart agentapi-hub`）。
  先更新 hub 再更新工作主機：較新的實例可能需要較新的 hub，例如模型 provider。
- **實例：**總覽的 Versions 卡片可以更新以 `--allow-update` 加入的主機，各自等 agent 空閒時進行
  （[從 hub 更新](hub.zh-TW.md#從-hub-更新)）。其他主機在本機執行 `agentapi update --restart`。
- **launcher：**`agentapi update` 之後執行 `systemctl --user restart agentapi-host`。它的 job 會繼續執行，
  由新的 launcher 接手。

<a id="架設問題"></a>

## 架設時常見的狀況

| 狀況 | 原因 | 處理 |
| --- | --- | --- |
| `hub join` 說 hub 沒有接受測試事件 | secret 不對、連不到 hub，或回應的是登入頁 | 確認 `HUB_SECRET`；在工作主機上 `curl -s -o /dev/null -w '%{http_code}' http://<hub>:3290/` 應該立即印出狀態碼；改用能直接連到的位址 |
| 登出後服務就停了 | 這個使用者沒有啟用 lingering | `sudo loginctl enable-linger <user>`，或改用 system unit |
| `agentapi host install` 說 `no systemd user instance` | 沒有 systemd 的容器 | 從工作區的啟動腳本執行 `agentapi host serve`（[`agentapi host`](cli.zh-TW.md#agentapi-host)） |
| hub 的 Versions 卡片要求一次性設定 | 執行檔所在的目錄使用者無法寫入 | 移到 `~/.local/bin`（[從 hub 更新](hub.zh-TW.md#從-hub-更新)） |
| `agentapi update` 因 GitHub 的 rate limit 失敗 | 多台主機共用一個對外 IP | 等一小時，或用 `gh release download` 下載 release，再對照 `checksums.txt` 檢查 |
| runner 的檢查回 `401` | `.env` 裡是別的 token，或改了 `.env` 後 runner 還沒重啟 | 修正 `.env`，再停止並啟動 runner 服務 |
| workflow 從來沒有執行 | runner 的 label 或 group 對不上，或中央 workflow 沒有開放使用 | 檢查 runner 的 label、group 開放的 repo，以及中央 repo 的 Access 設定 |
| job push `.github/workflows/` 被拒 | App 沒有 workflows 權限 | 在 App 的設定加上它，並在每個安裝處接受 |
| issue 停在 Starting，接著失敗 | launcher 無法 clone、agent 沒登入，或 job 的實例結束了 | issue 的詳細資料會顯示原因與 log；在那台主機執行 `agentapi host status` |

hub 開始運作之後的問題，見[先依錯誤現象找原因，再調整設定](hub.zh-TW.md#疑難排解)。
