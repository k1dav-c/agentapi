// Joining a machine to the hub from its page (the Add dialog, an empty
// hub's guide): where machines reach the hub (GET /api/join), and, for the
// URL picked, the command to run on the machine and a prompt for an agent
// there.

// JoinURL is a URL machines join the hub by, with the interface of its
// address on the hub's machine; none for the address the hub binds.
export type JoinURL = {url: string; interface?: string};

// JoinInfo is GET /api/join. Only a hub with a token sends its secret
// (secret_shown): one without would show it to everyone who reaches it.
export type JoinInfo = {urls: JoinURL[]; secret?: string; secret_shown: boolean};

// fetchJoinInfo asks the hub each time the page shows the steps, so the
// page holds the secret only while it shows them.
export async function fetchJoinInfo(root: string, fetcher: typeof fetch = fetch): Promise<JoinInfo> {
  const response = await fetcher(`${root}/api/join`, {credentials: "same-origin", cache: "no-store"});
  if (!response.ok) throw new Error(`The hub answered ${response.status}.`);
  const body = (await response.json()) as {urls?: unknown; secret?: unknown; secret_shown?: unknown};
  const urls = Array.isArray(body.urls) ? body.urls.filter((u): u is JoinURL => typeof u?.url === "string" && u.url !== "") : [];
  const secret = body.secret_shown === true && typeof body.secret === "string" && body.secret !== "" ? body.secret : undefined;
  return {urls, secret, secret_shown: secret !== undefined};
}

// joinChoices are the URLs to pick from: the hub's, the likeliest first;
// when it found none (or didn't answer), the page's own address.
export function joinChoices(info: JoinInfo | undefined, root: string): JoinURL[] {
  return info?.urls.length ? info.urls : [{url: `${root}/hooks`}];
}

// The first release whose join checks that the hub takes its events, and
// the first whose join takes --allow-update.
export const JOIN_CHECKS_SINCE = "1.6.1";
export const ALLOW_UPDATE_SINCE = "2.1.0";

// What shows in place of the secret, and what stands for one the hub
// doesn't show.
export const SECRET_MASK = "••••••••";
export const SECRET_PLACEHOLDER = "<hub secret>";

export type JoinOptions = {
  // The URL the machine joins by; others are the hub's other URLs, for a
  // machine on another network.
  hub: JoinURL;
  others: JoinURL[];
  // The hub's secret; none when the hub doesn't show it.
  secret?: string;
  // allowUpdate lets the hub update the machine's AgentAPIs.
  allowUpdate: boolean;
  // masked shows SECRET_MASK in place of the secret, for the screen.
  masked: boolean;
};

const shellQuote = (text: string) => `'${text.replaceAll("'", `'\\''`)}'`;
// shellWord is text as one word of a shell command, quoted if it must be.
const shellWord = (text: string) => (/^[\w@%+=:,./~-]+$/.test(text) ? text : shellQuote(text));

function secretText({secret, masked}: JoinOptions): string | undefined {
  if (secret === undefined) return undefined;
  return masked ? SECRET_MASK : secret;
}

// joinCommand is the command to run on the machine. The secret goes in
// through the environment, where agentapi hub join takes it from.
export function joinCommand(options: JoinOptions): string {
  const secret = shellQuote(secretText(options) ?? SECRET_PLACEHOLDER);
  const update = options.allowUpdate ? " --allow-update" : "";
  return `AGENTAPI_HUB_SECRET=${secret} agentapi hub join ${shellWord(options.hub.url)} --public-url 'http://<this machine>:{port}'${update}`;
}

const named = (url: JoinURL) => (url.interface ? `${url.url} (${url.interface})` : url.url);

// joinPrompt is what an agent on the machine needs to join it, as the
// agentapi-hub-join skill asks for it, with the steps for an agent without
// the skill and what it must not do: restart an AgentAPI (one runs it), or
// show the secret.
export function joinPrompt(options: JoinOptions): string {
  const {hub, others, allowUpdate} = options;
  const url = shellWord(hub.url);
  const secret = secretText(options);
  const alternatives = others.map(named);
  const since = allowUpdate ? ALLOW_UPDATE_SINCE : JOIN_CHECKS_SINCE;
  const otherURL = alternatives.length === 1 ? alternatives[0] : `the one it reaches of ${alternatives.join(", ")}`;
  const lines = [
    "Join this machine's AgentAPIs to my AgentAPI hub (agentapi hub join). If the agentapi-hub-join skill is available, follow it; here are the values it asks for:",
    `- The hub's webhook URL: ${hub.url}${hub.interface ? ` (the hub's ${hub.interface})` : ""}.` +
      (alternatives.length > 0 ? ` If this machine isn't on that network, use ${otherURL} instead, in the steps below too.` : ""),
    secret === undefined
      ? "- The hub secret: ask me for it (the one given to agentapi hub serve). Pass it to agentapi hub join only through the environment; never print it, or write it to a file or a commit."
      : `- The hub secret: \`${secret}\`. Pass it to agentapi hub join only through the environment; never print it, or write it to a file or a commit.`,
    "- Where the hub reaches this machine: http://<this machine's IP>:{port}, with the IP this machine has on the network it shares with the hub (hostname -I or ip -4 addr; ifconfig on macOS). Keep {port} as it is.",
    allowUpdate ? "- Updates from the hub: yes, join with --allow-update (see Updates below)." : "- Updates from the hub: no, join without --allow-update.",
    "",
    "Steps:",
    `1. agentapi --version: below ${since}, run sudo agentapi update first.`,
    "2. Check that this machine reaches the hub; it must answer 401:",
    `   curl -s -o /dev/null -w '%{http_code}\\n' -X POST ${url}`,
    alternatives.length > 0
      ? "   000 or a timeout: no route to the hub, so try the other URL above. 302, 303 or an HTML page: a sign-in page is in the way. If none answers 401, stop and tell me."
      : "   000 or a timeout: no route to the hub. 302, 303 or an HTML page: a sign-in page is in the way. Then stop and tell me.",
    "3. Join. It sends the hub a signed test event first; if that fails, follow its message, and don't use --no-check:",
    `   AGENTAPI_HUB_SECRET=${shellQuote(secret ?? SECRET_PLACEHOLDER)} agentapi hub join ${url} --public-url 'http://<IP>:{port}'${allowUpdate ? " --allow-update" : ""}`,
    `4. agentapi hub status: each running AgentAPI should report to ${hub.url}.`,
    "5. If the AgentAPIs weren't started with --allowed-hosts '*' (the Coder module's are), add the IP to ~/.agentapi/config.yaml; it takes effect when they next start:",
    '   allowed-hosts: [localhost, 127.0.0.1, "[::1]", <IP>]',
  ];
  if (allowUpdate) {
    lines.push(
      "",
      "Updates: if join prints that the hub can't update them yet because AgentAPI can't replace its binary, explain the one-time setup it prints (it copies the binary to ~/.local/bin and leaves a symlink in its place, with sudo) and ask me before you run it. Also suggest this line in ~/.agentapi/config.yaml, so an update keeps each AgentAPI's conversation, and ask me before you add it:",
      "   state-db: ~/.agentapi/state/{port}.db",
    );
  }
  lines.push(
    "",
    "Rules: don't restart or stop any AgentAPI (one of them runs this conversation); list the ones that need a restart and let me decide. Don't print, log or commit the secret.",
    "",
    `Report: the hub URL and the IP you used, agentapi --version, each AgentAPI's join result, ${allowUpdate ? "whether the hub can update them, " : ""}and which ones need a restart.`,
  );
  return lines.join("\n");
}
