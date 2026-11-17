# Security

English | [繁體中文](security.zh-TW.md)

Protect Tendyard before other people can reach its port: anyone with API access can use the
agent's capabilities, and there are no individual users or roles.

- **Restrict connections:** choose a bind address or a network boundary that reaches only trusted clients.
- **Require credentials:** enable an API token before sharing access; authentication is off by default.
- **Constrain browser access:** allowed hosts and origins check browser requests; they do not replace network access controls.
- **Review capabilities:** access includes sending work, using the terminal and reading workspace files.

By default, the server listens on every network interface and accepts `Host` headers naming
`localhost` or an IP address. The host check prevents DNS rebinding from web pages, but network
programs can send any `Host` header. Tendyard logs a startup warning when it takes no token and
listens on an address other machines can reach.

<a id="bind-address"></a>

## The bind address limits which network interfaces accept connections

By default, the server listens on all network interfaces. Use `--bind` (or `AGENTAPI_BIND`) to
listen on one address only, e.g. loopback so nothing else on the network can connect. It takes an IP
address or hostname without a port; the port stays `--port`.

```bash
agentapi server --bind 127.0.0.1 -- claude
agentapi server --bind ::1 -- claude          # IPv6 loopback
AGENTAPI_BIND=127.0.0.1 agentapi server -- claude
```

`--bind` decides who can connect; `--allowed-hosts` below decides which `Host` headers are accepted
from those who can.

<a id="api-token-authentication"></a>

## An API token restricts access to clients that hold it

All API endpoints can be protected with a Bearer token. Authentication is **disabled by default**
for backward compatibility.

```bash
# Auto-generate a random token (printed to stderr on startup)
agentapi server --api-token -- claude

# Use a specific token
agentapi server --api-token=my-secret -- claude

# Via environment variable
AGENTAPI_API_TOKEN=my-secret agentapi server -- claude
```

A generated token stays the same across a restart in place (SIGUSR2, `POST /update`,
`agentapi restart`), so clients stay signed in; a new `agentapi server` generates a new one.

With authentication on, every API request must include `Authorization: Bearer <token>` or the chat UI's
session cookie. Static file routes (`/`, `/chat/*`) are exempt so browsers can open the chat UI
without a token. Tokens are compared in constant time.

The chat UI signs in by trading the token for a cookie, since browsers can't add an `Authorization`
header to the event stream or to images:

- Open `http://localhost:3284/chat/embed#token=<token>`. With an auto-generated
  token the server prints this link on startup. The token is in the URL
  fragment, which the browser never sends to the server, a proxy or an access
  log; the page exchanges it and removes it from the address bar. A link to a
  server without `--api-token` just opens the chat.
- Or just open the chat: when the server answers 401 it asks for the token.
  The dialog names the server that asks and warns when it isn't the page's
  own origin, and says so when the browser didn't keep the session cookie.
  After Cancel, **Reconnect now** asks again.

`POST /auth/session` with `Authorization: Bearer <token>` sets the cookie
(`agentapi_session_<hash>`, HttpOnly, SameSite=Strict, `Path=/`, and Secure over HTTPS or behind a
proxy that sends `X-Forwarded-Proto: https`). It holds an HMAC of the token, not the token, so
changing the token signs every browser out. Its name comes from the token, so servers with
different tokens on one host keep their own sign-ins. `DELETE /auth/session` signs out, and
`GET /auth/session` (no credentials needed) reports `auth_required` and `authenticated`. Because
the cookie is SameSite=Strict, pages on other sites can't make requests with it. A chat UI served
from another origin (e.g. `bun run dev` on port 3000) needs that origin in `--allowed-origins`;
`--allowed-origins '*'` doesn't allow requests with cookies. A different port is not a different
site, and the default allowed origins include `http://localhost:3000` and `http://localhost:3001`
for chat development. On a shared machine, set `--allowed-origins` to the origins you use, so a
signed-in browser's cookie only works for them.

<a id="allowed-hosts"></a>

## Allowed hosts reject unrecognized Host headers

By default, the server only allows requests with the `Host` header set to `localhost`, `127.0.0.1`
or `[::1]`, or to any other IP address: DNS rebinding needs a name the attacker's page controls, so
an address (a LAN's, a VPN's, one DHCP changes) needs no entry. To open Tendyard by a name, set
`AGENTAPI_ALLOWED_HOSTS` or `--allowed-hosts`. A network such as `192.168.9.0/24` isn't accepted.
Hosts must be hostnames only (no ports); the server ignores the port portion of incoming requests
when authorizing. This checks the `Host` header only; to keep the server off the network
altogether, also listen on loopback with `--bind 127.0.0.1` (see [The bind address limits which network interfaces accept connections](#bind-address)).

To allow requests from any host, use `*` as the allowed host.

```bash
agentapi server --allowed-hosts '*' -- claude
```

To allow a specific host, use:

```bash
agentapi server --allowed-hosts 'example.com' -- claude
```

To specify multiple hosts, use a comma-separated list in the `--allowed-hosts` flag, or a
space-separated list in the `AGENTAPI_ALLOWED_HOSTS` environment variable.

```bash
agentapi server --allowed-hosts 'example.com,example.org' -- claude
# or
AGENTAPI_ALLOWED_HOSTS='example.com example.org' agentapi server -- claude
```

<a id="allowed-origins"></a>

## Allowed origins control browser access across origins

By default, the server allows CORS requests from `http://localhost:3284`, `http://localhost:3000`,
and `http://localhost:3001`. Set `AGENTAPI_ALLOWED_ORIGINS` or `--allowed-origins` to change which
origins may make cross-origin requests.

To allow requests from any origin, use `*` as the allowed origin:

```bash
agentapi server --allowed-origins '*' -- claude
```

To allow a specific origin, use:

```bash
agentapi server --allowed-origins 'https://example.com' -- claude
```

To specify multiple origins, use a comma-separated list in the `--allowed-origins` flag, or a
space-separated list in the `AGENTAPI_ALLOWED_ORIGINS` environment variable. Origins must
include the protocol (`http://` or `https://`) and may use wildcards (e.g.,
`https://*.example.com`):

```bash
agentapi server --allowed-origins 'https://example.com,http://localhost:3000' -- claude
# or
AGENTAPI_ALLOWED_ORIGINS='https://example.com http://localhost:3000' agentapi server -- claude
```

The server blocks changes from a page on another origin unless that origin is listed. This
covers methods other than GET, HEAD and OPTIONS. CORS alone would only stop the page from reading
the response: browsers can send forms or `fetch` requests in `no-cors` mode without a preflight.
The server therefore checks the `Sec-Fetch-Site` and `Origin` headers as well. Without that check
or `--api-token`, a site you open could send the agent a task or type into the terminal.
`*` lets every page read responses, but doesn't authorize these changes; list the origins that
need them. Non-browser clients (curl, SDKs, the hub) aren't affected by this browser-origin check.

<a id="what-an-api-client-can-do"></a>

## An authenticated client can use the agent’s capabilities

- Have the agent run anything it can. Agents start in the bypass
  permission mode, where tools run without asking, unless the command line
  picks another mode or Claude Code runs as root outside a sandbox (it then
  starts in `default`). Claude Code gets `--allow-dangerously-skip-permissions`
  (except as root outside a sandbox), so a client can switch any session to
  bypass. Codex's default overrides the policies in `~/.codex/config.toml`
  unless the command line picks one.
- Run commands in the [terminal](agents.md#terminal) (on by default;
  `--terminal=false` turns it off).
- Read files in the agent's working directory with the file viewer endpoints;
  paths outside it, even through symlinks, are refused, and the diff only
  reads the git repository. An HTML preview's scripts run in a sandbox
  without access to the chat or the API. `GET /files/find` lists the paths of
  files below the working directory whose path ends with the one asked for.
- Read the conversations, subagent steps and costs of the agent's earlier
  sessions in its working directory from the agent's own logs (`~/.claude`,
  `~/.codex`, `~/.pi`), and open any of those sessions.
- Upload files of up to 100 MB; only images are served back.
- Change the webhook and restart the agent. The webhook can send the
  conversation (`message.created`, `tool.completed`) to any URL; `GET /webhook` never returns its
  secret.
- With `--allow-update`, [update Tendyard](api.md#updating-through-the-api) to the latest official
  release and restart it. A client can't choose the version or where it comes from.

Use `--api-token` together with `--bind` or a reverse proxy whenever someone else can reach the
server.
