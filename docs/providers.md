# Model providers

English | [繁體中文](providers.zh-TW.md)

Claude Code and Codex can switch to another model provider while they run: an endpoint that speaks
the Anthropic Messages API (Claude Code) or the OpenAI Responses API (Codex), such as a LiteLLM or
vLLM gateway. One gateway that speaks both serves both agents. Define providers in the machine's
`~/.agentapi/config.yaml` (`--config`):

```yaml
providers:
  - id: kimi                      # lowercase letters, digits, - and _
    name: Kimi K3                 # optional
    base_url: https://gateway.example.com/v1
    api_key: sk-...               # optional
    models: [Kimi-K3-NVFP4]       # optional: by default, those GET <base_url>/models lists
```

Codex calls `<base_url>/responses`. Claude Code calls the same URL without `/v1`, plus
`/v1/messages`; set `anthropic_base_url` when its endpoint is elsewhere. A `base_url` without a
version gets `/v1` for Codex. AgentAPI reads the file again when it changes. Keep it readable by
you only (`chmod 600`): it holds the keys, and AgentAPI warns when others can read it.

A hub keeps providers for every machine that reports to it (see
[Running a hub](api.md#running-a-hub)). On a machine, a provider in its config file wins over the hub's
with the same `id`.

`GET /settings` lists the providers under `options.providers`, with their models, and the setting
`provider` says which one the agent runs on: `default` for its own (Claude Code's Anthropic login or
key, Codex's OpenAI login or its config's `model_provider`). Switch with `PUT /settings`:

```bash
curl -X PUT localhost:3284/settings -d '{"provider": "kimi", "model": "Kimi-K3-NVFP4"}'
curl -X PUT localhost:3284/settings -d '{"provider": "default"}'
```

Without `model`, the agent takes the provider's first model; back on its own provider, the model
it had before. On a provider, `{"model": ...}` picks among that provider's models. In the chat, the
model menu lists the agent's own models, then each provider's.

![The model menu of Claude Code: its own models, then those of two providers from the hub, each under its name and host](screenshots/model-providers.png)

The two agents switch differently:

- **Claude Code** switches while it runs, from its next API call, also during a turn. Going back to
  its own provider starts it again on the same session, which takes a few seconds; during a turn it
  is refused (409). If a switch fails, AgentAPI restores the previous environment and model. If an
  answer is unconfirmed or restoration fails, it starts a replacement on the previous known provider
  instead of allowing another turn with settings that might not match the UI.
- **Codex** binds a thread to its provider, and an OpenAI thread holds items another provider
  refuses (OpenAI's tool definitions, encrypted reasoning). A switch starts app-server again on the
  other provider, in a new thread that gets the conversation's messages as text, without the tool
  calls. During a turn the switch is refused (409).

The chosen provider applies to every agent that starts in the session, also after an AgentAPI
restart with `--state-db`. A provider that is no longer defined is dropped, with a warning: the
agent starts on its own provider. The hub's providers are kept beside the config file
(`hub-providers.json`, sealed with the hub's secret) for the next start, so a session on one of
them starts on it again while the hub can't be reached. Until the hub answers, a chosen provider
that may be one of the hub's stays chosen while the agent starts on its own provider; the agent goes
back to it at its first start after the hub answered.

A provider's models (those it lists at `<base_url>/models`) are kept for 10 minutes, then listed
again in the background. `GET /settings` waits 3 seconds at most for a provider that doesn't
answer, and doesn't ask it again for a minute.

An agent on a provider can read that provider's key: it runs as your user, with the key in its
environment. Give a provider only a key you would give the agent.

Model calls on a provider have no known price, so they add tokens but no cost (`--prices` adds
prices). `GET /sessions`, `GET /cost` and the summary's `spend` count each provider's calls, tokens
and cost apart (`providers`, `default` for the agent's own). Claude Code's log doesn't say which
endpoint a call went to, so AgentAPI records in `~/.agentapi/provider-ledger.jsonl` which provider
each session ran on, and from when.
