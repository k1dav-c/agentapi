# hub

[English](hub.md) | [繁體中文](hub.zh-TW.md)

hub 在一個頁面上監看多台機器的 AgentAPI：列出每個實例，需要你處理的排最前面，點一下就能在聊天
介面中開啟，桌面和手機都能用。hub 就在同一個 binary 裡：`agentapi hub serve`。

![hub：所有實例、需要你處理的排最前面，以及在聊天介面中開啟的一個](screenshots/hub-desktop.png)

[README](../README.zh-TW.md#執行-hub) 有精簡版說明。這份指南一步步說明怎麼架設、每種狀態的意思、
各個 token、疑難排解，以及 hub 的 API。

## 目錄

- [運作方式](#運作方式)
- [架設](#架設)
- [使用 hub](#使用-hub)
- [Token 與安全性](#token-與安全性)
- [疑難排解](#疑難排解)
- [參考](#參考)
- [限制](#限制)

## 運作方式

```text
 手機／桌面瀏覽器 ──HTTPS──▶ hub（agentapi hub serve，放在常開的機器上）
                               ▲ 有簽章的 webhook 事件    │ 摘要串流和聊天介面，
                               │ （instance.*、task.*…）  ▼ 帶著各實例的 token
          ┌────────────────────┴───────────┬────────────────────────────────┐
   workspace A 上的 AgentAPI      workspace B 上的 AgentAPI       你筆電上的 AgentAPI
```

hub 和各個 AgentAPI 透過你掌控的網路互連，例如你自己的 VPN。hub 完全不必放上公開網路。

1. AgentAPI 用 webhook 加入。`agentapi hub join` 把 hub 的位址和 secret 寫進
   `~/.agentapi/config.yaml`，這個使用者的每個 `agentapi server` 啟動時都會讀取。之後每個
   AgentAPI 都送出有簽章的 `instance.started`、`instance.heartbeat` 和 `instance.stopped`，
   hub 據此新增、更新或停止實例。
2. hub 追蹤每個實例的摘要（`GET /events?only=summary`）：狀態、在等什麼、標題、最新任務、佇列和
   用量限制。每個實例一條連線；斷線時 1 秒後重連，之後間隔逐次拉長，最多 30 秒。串流 45 秒沒有任何
   動靜就視為斷線。
3. 瀏覽器用 hub token 登入，從一條串流取得所有實例和每次變更。
4. 開啟實例時，聊天介面透過 hub 載入（`/i/<id>/…`）。hub 轉送每個請求時，用實例的 API token 取代
   瀏覽器的憑證。

## 架設

範例用三個虛構的名稱：hub 在 `hub.vpn`，AgentAPI 在 `dev-1.vpn` 和 `dev-2.vpn`。請換成你機器的
VPN 名稱或位址。

### 1. 產生 secret

最多需要三個值。不要留在 shell 歷史紀錄或儲存庫裡，例如放進密碼管理工具：

```bash
openssl rand -hex 32   # HUB_TOKEN：登入用
openssl rand -hex 32   # HUB_SECRET：AgentAPI 為事件簽章用
```

第三個是 AgentAPI 自己的 API token（`--api-token`），有設定的話才需要，hub 會把它送給各實例。所有
實例可以共用一個；手動加入的實例也可以各有各的。

### 2. 執行 hub

挑一台會一直開著、而且 AgentAPI 和你的手機都能透過 VPN 連到的機器：

```bash
agentapi hub serve --bind hub.vpn --token "$HUB_TOKEN" --secret "$HUB_SECRET" \
  --instance-token "$AGENTAPI_TOKEN"
```

hub 監聽 port 3290，並把實例保存在 `~/.agentapi/hub/instances.json`，重新啟動後仍然記得。
`--bind` 讓 hub 只聽 VPN 介面；不加的話監聽所有介面。這些值也可以改用 `AGENTAPI_HUB_TOKEN`、
`AGENTAPI_HUB_SECRET` 和 `AGENTAPI_HUB_INSTANCE_TOKEN` 設定。

要讓 hub 持續執行，就用這台機器啟動其他服務的方式啟動它。例如 systemd 的使用者 unit：

```ini
# ~/.config/systemd/user/agentapi-hub.service
[Service]
EnvironmentFile=%h/.config/agentapi-hub.env
ExecStart=/usr/local/bin/agentapi hub serve --bind hub.vpn
Restart=on-failure

[Install]
WantedBy=default.target
```

`agentapi-hub.env` 放三個 `AGENTAPI_HUB_*` 變數；只讓擁有者能讀取。

### 3. 讓每台機器的 AgentAPI 加入

在每台要讓 hub 監看的機器上執行一次：

```bash
sudo agentapi update   # 每台機器都需要 AgentAPI 1.3 以上
agentapi hub join http://hub.vpn:3290/hooks --secret "$HUB_SECRET" \
  --public-url 'http://dev-1.vpn:{port}'
```

- `join` 把 hub 寫進 `~/.agentapi/config.yaml`。這個使用者的每個 `agentapi server` 啟動時都會讀
  這個檔案，所以由腳本以固定參數啟動的 AgentAPI（例如 Coder workspace 的啟動腳本）不必改腳本也能
  加入。旗標和 `AGENTAPI_*` 環境變數仍然優先於這個檔案。
- `--public-url` 是 hub 連回各 AgentAPI 的網址。`{port}` 會換成各自的 port，所以同一行設定可以
  同時用在 3286 的 Claude Code 和 3285 的 Codex。
- 正在執行的 AgentAPI 會立刻套用，一秒內就出現在 hub 上。1.2 之前啟動的 AgentAPI 沒有在這台機器
  上登記，`join` 找不到它們：重新啟動它們或整個 workspace 一次。
- hub 轉送請求時，`Host` 標頭是實例的 VPN 名稱。AgentAPI 預設只接受 `localhost`、`127.0.0.1` 和
  `[::1]`，所以要允許它的 VPN 名稱：用 `--allowed-hosts '*'` 啟動（Coder module 就是這樣做），
  或把名稱加進設定檔：

  ```yaml
  allowed-hosts: [localhost, 127.0.0.1, "[::1]", dev-1.vpn]
  ```

用這兩個指令確認：

```bash
agentapi hub status   # 設定檔裡的 hub，以及每個執行中的 AgentAPI 回報到哪裡
agentapi list         # 這台機器上執行中的 AgentAPI，以及各自的摘要
```

### 4. 開啟 hub

在連上 VPN 的桌面或手機開啟 `http://hub.vpn:3290/`，用 `HUB_TOKEN` 登入。登入狀態維持 30 天。

## 使用 hub

### 登入

![hub 的登入畫面](screenshots/hub-sign-in.png)

token 只送給 hub 一次，換回一個由 token 推導出的 HttpOnly cookie；頁面不會保存 token。更換
`--token` 會讓所有瀏覽器登出。

### 清單

![清單會顯示的每種狀態](screenshots/hub-list.png)

每一列顯示實例的名稱（`--name`，預設為工作目錄名稱）、目前在做什麼、標題（工作階段名稱，沒有的話
是最新提示的開頭）、agent、機器、佇列中等待的訊息數，以及用得最多的用量限制。各列依下表的狀態順序
排列，同狀態再依名稱排序：

| 圓點 | 標示 | 意思 |
| --- | --- | --- |
| 琥珀色 | Needs your answer | 有核准、提問、計畫或終端機對話框在等你 |
| 琥珀色 | Needs login | 要先在它的機器上登入，agent 才能工作 |
| 琥珀色 | Usage limit | 用量限制停下了回合；佇列中的訊息等重置後才送出 |
| 閃爍的藍色 | Working | agent 正在處理任務 |
| 綠色 | Finished | 上次有人查看這個實例之後，有任務結束了 |
| 灰色 | Idle | 沒有工作 |
| 閃爍的灰色 | Connecting | hub 正在開啟它的摘要串流 |
| 紅色 | Unreachable | hub 連不到它；標題的位置改顯示原因 |
| 空心 | Stopped、Agent exited | AgentAPI 已停止，或它的 agent 已結束 |

頁首會顯示實例數和需要你處理的數量。分頁標題也會顯示，例如 `(2) AgentAPI hub`，釘選的分頁也看得到。

「Finished」依據實例的未讀標記，這個標記由該 AgentAPI 的所有用戶端共用：在 hub 或實例自己的聊天
介面開啟它，各處都會變成已讀。

### 開啟實例

點選一列，就會在清單旁開啟這個實例的聊天介面：聊天介面的功能在這裡都能用，包括回覆請求。網址會記住
目前的實例（`#<id>`），重新整理或從書籤開啟都會回到同一個實例。

### 在手機上

| 清單 | 實例 |
| --- | --- |
| ![手機上的 hub 清單](screenshots/hub-phone.png) | ![在手機上開啟的實例](screenshots/hub-phone-chat.png) |

在窄螢幕上，清單和聊天介面輪流顯示；上方的 **Instances** 可以返回清單。在手機上回覆核准：

![在手機上透過 hub 回覆核准](screenshots/demo-hub.gif)

### 加入與移除實例

![用網址加入實例](screenshots/hub-add.png)

**+** 可以用網址加入 AgentAPI，適用於無法執行 `agentapi hub join` 的實例（舊版，或你不會登入的
機器）。hub 會先向它要摘要，所以它必須連得到，而且是 AgentAPI 1.1 以上。在這裡輸入的 token 會為
這個實例保存，取代 `--instance-token` 送出。這樣加入的實例不會回報啟動或停止；hub 只會發現它連得到
或連不到。

已停止或連不到的那一列，滑鼠移過去會出現 **Forget**，可以把它從 hub 移除。用 webhook 加入的實例，
下次啟動時會再出現。

## Token 與安全性

| 值 | 誰持有 | 用途 |
| --- | --- | --- |
| hub token（`--token`） | 你和 hub | 讓瀏覽器登入 hub |
| hub secret（`--secret`） | hub 和每台機器的設定檔 | 為 AgentAPI 的事件簽章，只有你的機器能新增實例 |
| 實例 token（`--instance-token`，或手動加入時各自的） | hub 和各 AgentAPI | 讓 hub 呼叫 AgentAPI；瀏覽器永遠拿不到 |

- hub 只接受具備有效 `X-AgentAPI-Signature-256`（`<timestamp>.<body>` 的 HMAC-SHA256）、
  timestamp 誤差不超過 10 分鐘、而且 delivery ID 沒處理過的事件。這很重要：事件裡的網址，就是
  hub 送出實例 token 的地方。
- session cookie 是 HttpOnly、SameSite=Lax，由 hub token 推導而來，裡面沒有 token 本身。代理會
  移除瀏覽器的 cookie 以及 `Authorization`、`Origin`、`Referer` 標頭，也會移除實例的
  `Set-Cookie`，雙方的憑證都不會傳到另一邊。
- `~/.agentapi/hub/instances.json` 保存手動加入的實例的 token，建立時只有擁有者能讀取。
  `~/.agentapi/config.yaml` 保存 hub secret，`agentapi hub join` 也以同樣的權限寫入。
- 登入時會送出 hub token。hub 只開在 VPN 上，或放在 HTTPS 後面。
- 登入 hub 的人能做的事，等同各 AgentAPI 允許的事：送任務、回覆核准、使用終端機。hub token 只給
  你也願意給實例 token 的人。

## 疑難排解

| 看到的狀況 | 原因 | 處理方式 |
| --- | --- | --- |
| 實例一直沒出現 | AgentAPI 在 `join` 之前、或 1.2 之前就啟動了 | 重新啟動它，或重開它的 workspace |
| 實例一直沒出現；hub 的 log 有「Refused an event」 | `join` 和 hub 的 `--secret` 不同，或兩邊時鐘差超過 10 分鐘 | 用 hub 的 secret 重新執行 `agentapi hub join`；檢查時鐘 |
| 實例一直沒出現；`agentapi hub status` 顯示沒有 hub | `join` 寫入的設定檔，不是 AgentAPI 讀的那個 | 用同一個使用者，或兩邊傳同一個 `--config` |
| Unreachable：`400 Bad Request` | AgentAPI 拒絕以 VPN 名稱作為 `Host` | 在 `--allowed-hosts` 允許它（步驟 3） |
| Unreachable：`needs an API token` | AgentAPI 以 `--api-token` 執行，hub 給的 token 不同 | 給 hub `--instance-token`，或加入時輸入 token |
| Unreachable：connection refused 或逾時 | `--public-url` 錯誤，或 hub 連不上 VPN | 在 hub 的機器上用 `curl <url>/summary` 檢查網址 |
| Unreachable，而且 hub 的 VPN 用戶端以 userspace 模式執行 | hub 的連線沒有經過 VPN | 為 hub 設定 `ALL_PROXY`，指向 VPN 用戶端的 SOCKS5 proxy |
| 加入時出現「is it AgentAPI 1.1 or later?」 | 這個實例沒有摘要 | 用 `agentapi update` 更新它 |
| 頁面說 hub 沒有回應 | 位址錯誤，或 hub 沒有執行 | 開啟 `http://<hub>:3290/api/session`；hub 執行中會回傳 JSON |
| 透過 Coder 的 port 網址開啟時，先要求登入 Coder | Coder 的 port 網址需要登入 Coder | 在那個瀏覽器先登入 Coder，再登入 hub |

在實例所在的機器上執行 `agentapi hub status`，可以看到設定檔指向哪個 hub，以及每個執行中的
AgentAPI 回報到哪裡。hub 的 log 會記下開始追蹤、已停止和連不到的實例（附上錯誤），以及拒絕的事件；
AgentAPI 的 log 會記下傳送失敗的 webhook。

## 參考

### `agentapi hub serve`

| 旗標 | 預設值 | 說明 |
| --- | --- | --- |
| `--token` | `AGENTAPI_HUB_TOKEN` | 瀏覽器登入用的 token；必填 |
| `--secret` | `AGENTAPI_HUB_SECRET` | AgentAPI 為事件簽章用的 secret；必填 |
| `--instance-token` | `AGENTAPI_HUB_INSTANCE_TOKEN`，否則 `AGENTAPI_API_TOKEN` | 送給 AgentAPI 的 API token |
| `--state` | `~/.agentapi/hub/instances.json` | 保存實例的檔案 |
| `--port` | `3290` | 監聽的 port |
| `--bind` | 所有介面 | 監聽的位址，例如 VPN 位址 |

hub 透過環境變數中的 proxy（`HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`）連到實例。

### `agentapi hub join`、`leave`、`status`

| 旗標 | 預設值 | 說明 |
| --- | --- | --- |
| `--secret` | `AGENTAPI_HUB_SECRET` | 為事件簽章的 secret；沒有的話 hub 會拒絕事件 |
| `--public-url` | | hub 連回 AgentAPI 的網址；`{port}` 和 `{hostname}` 會被填入 |
| `--events` | `instance.*`、`task.*`、`request.*`、`login.*`、`limit.*`、`agent.error` | 送給 hub 的事件 |
| `--api-token` | `AGENTAPI_API_TOKEN` | 給需要 token 的執行中 AgentAPI |
| `--config` | `~/.agentapi/config.yaml` | 要寫入的設定檔 |
| `--registry-dir` | `~/.agentapi/instances` | 執行中 AgentAPI 的登記目錄 |

`join` 把 `webhook-url`、`webhook-secret`、`webhook-events` 和 `public-url` 寫進設定檔，並用
`PUT /webhook` 套用到執行中的 AgentAPI，保留各自的逾時和重試次數。`leave` 移除這些設定，並關掉執行中
AgentAPI 的 webhook。當下無法修改的 AgentAPI，會在重新啟動時套用設定檔。

### hub 的 HTTP API

| 端點 | 說明 |
| --- | --- |
| `POST /hooks` | AgentAPI 的 webhook 事件；以 `--secret` 簽章，不需要登入 |
| `GET /api/session` | 請求帶有 session 或 hub token 時回傳 `{"authenticated": true}` |
| `POST /api/session` | `{"token": …}` 登入並設定 session cookie |
| `DELETE /api/session` | 登出 |
| `GET /api/instances` | 所有實例，含摘要和連線狀態 |
| `POST /api/instances` | `{"url": …, "token": …}` 以網址加入實例 |
| `DELETE /api/instances/{id}` | 移除實例 |
| `GET /api/events` | Server-sent events：先送 `instances`（全部），再送 `instance`（某個有變動）、`removed`（`{"id"}`），以及每 15 秒一次的 `heartbeat` |
| `/i/{id}/…` | 透過 hub 存取實例自己的 API 和聊天介面 |

除了 `/hooks` 和 `/api/session`，每個端點都需要 session cookie 或
`Authorization: Bearer <hub token>`。任何回應裡都不會出現實例 token。

### 檔案

| 檔案 | 寫入者 | 內容 |
| --- | --- | --- |
| `~/.agentapi/config.yaml` | `agentapi hub join` | hub 的 webhook，每個 `agentapi server` 都會讀取 |
| `~/.agentapi/instances/` | `agentapi server` | 這台機器上執行中的 AgentAPI，供 `join` 和 `agentapi list` 使用 |
| `~/.agentapi/hub/instances.json` | `agentapi hub serve` | hub 記得的實例，以及手動加入的實例的 token |

## 限制

- hub 對所有實例都提供它自己版本的聊天介面。hub 和各實例請使用相同的 AgentAPI 版本。
- 所有人共用一個 hub token：沒有個別帳號或權限。
- hub 只監看和開啟 AgentAPI，不會啟動或停止它們。
- 關掉 hub 的頁面後，手機就收不到任何通知：hub 還不會送推播。每個 AgentAPI 只有一個 webhook，而
  hub 已經用掉了。
