import {describe, expect, test} from "bun:test";
import {
  SECRET_MASK,
  SECRET_PLACEHOLDER,
  fetchJoinInfo,
  joinChoices,
  joinCommand,
  joinPrompt,
  type JoinInfo,
  type JoinOptions,
} from "./hub-join";

const SECRET = "9f3a51c0de7b44e2";
const lan = {url: "http://192.168.9.210:3290/hooks", interface: "eth0"};
const vpn = {url: "http://100.116.90.98:3290/hooks", interface: "tailscale0"};
const options = (overrides: Partial<JoinOptions> = {}): JoinOptions => ({hub: lan, others: [vpn], secret: SECRET, allowUpdate: false, masked: false, ...overrides});

describe("fetchJoinInfo", () => {
  const answer = (status: number, body: unknown) => (async () => new Response(JSON.stringify(body), {status})) as unknown as typeof fetch;

  test("the hub's URLs and its secret, when it shows it", async () => {
    let asked = "";
    const fetcher = (async (url: string) => {
      asked = url;
      return new Response(JSON.stringify({urls: [lan, vpn], secret: SECRET, secret_shown: true}));
    }) as unknown as typeof fetch;
    expect(await fetchJoinInfo("http://hub:3290", fetcher)).toEqual({urls: [lan, vpn], secret: SECRET, secret_shown: true});
    expect(asked).toBe("http://hub:3290/api/join");
  });

  test("no secret from a hub without a token", async () => {
    expect(await fetchJoinInfo("", answer(200, {urls: [lan], secret_shown: false}))).toEqual({urls: [lan], secret: undefined, secret_shown: false});
  });

  test("an error, when the hub refuses", async () => {
    await expect(fetchJoinInfo("", answer(401, {error: "sign in to the hub"}))).rejects.toThrow("401");
  });
});

describe("joinChoices", () => {
  test("the hub's URLs, else the page's own address", () => {
    const info: JoinInfo = {urls: [lan, vpn], secret_shown: false};
    expect(joinChoices(info, "https://hub.example.com")).toEqual([lan, vpn]);
    expect(joinChoices({urls: [], secret_shown: false}, "https://hub.example.com")).toEqual([{url: "https://hub.example.com/hooks"}]);
    expect(joinChoices(undefined, "http://hub.vpn:3290")).toEqual([{url: "http://hub.vpn:3290/hooks"}]);
  });
});

describe("joinCommand", () => {
  test("the URL picked, with the secret through the environment", () => {
    expect(joinCommand(options())).toBe(
      `AGENTAPI_HUB_SECRET='${SECRET}' agentapi hub join http://192.168.9.210:3290/hooks --public-url 'http://{ip}:{port}'`,
    );
    expect(joinCommand(options({hub: vpn, others: [lan]}))).toContain("agentapi hub join http://100.116.90.98:3290/hooks ");
  });

  test("--allow-update, when the hub may update the machine", () => {
    expect(joinCommand(options({allowUpdate: true}))).toEndWith("--public-url 'http://{ip}:{port}' --allow-update");
    expect(joinCommand(options())).not.toContain("--allow-update");
  });

  test("a placeholder for a secret the hub doesn't show", () => {
    expect(joinCommand(options({secret: undefined}))).toStartWith(`AGENTAPI_HUB_SECRET='${SECRET_PLACEHOLDER}' agentapi hub join`);
  });

  test("a secret quoted for the shell", () => {
    expect(joinCommand(options({secret: "it's $HOME"}))).toStartWith(`AGENTAPI_HUB_SECRET='it'\\''s $HOME' agentapi`);
  });

  test("masked, the secret never shows", () => {
    const masked = joinCommand(options({masked: true}));
    expect(masked).not.toContain(SECRET);
    expect(masked).toStartWith(`AGENTAPI_HUB_SECRET='${SECRET_MASK}' agentapi hub join`);
  });
});

describe("joinPrompt", () => {
  test("the values the skill asks for, and the steps with the URL picked", () => {
    const prompt = joinPrompt(options());
    expect(prompt).toStartWith("Join this machine's AgentAPIs to my AgentAPI hub (agentapi hub join). If the agentapi-hub-join skill is available, follow it");
    expect(prompt).toContain(
      "- The hub's webhook URL: http://192.168.9.210:3290/hooks (the hub's eth0). If this machine isn't on that network, use http://100.116.90.98:3290/hooks (tailscale0) instead",
    );
    expect(prompt).toContain(`- The hub secret: \`${SECRET}\`. Pass it to agentapi hub join only through the environment`);
    expect(prompt).toContain("- Where the hub reaches this machine: --public-url 'http://{ip}:{port}', exactly as written. Don't look up the IP:");
    expect(prompt).toContain("follows it when the address changes");
    expect(prompt).not.toContain("hostname -I");
    expect(prompt).not.toContain("<IP>");
    expect(prompt).toContain("1. agentapi --version: below 2.2.0, run sudo agentapi update first.");
    expect(prompt).toContain("curl -s -o /dev/null -w '%{http_code}\\n' -X POST http://192.168.9.210:3290/hooks\n");
    expect(prompt).toContain(`AGENTAPI_HUB_SECRET='${SECRET}' agentapi hub join http://192.168.9.210:3290/hooks --public-url 'http://{ip}:{port}'\n`);
    expect(prompt).toContain("It prints the address {ip} stands for now.");
    expect(prompt).toContain("4. agentapi hub status: each running AgentAPI should report to http://192.168.9.210:3290/hooks.");
    expect(prompt).not.toContain("allowed-hosts");
    expect(prompt).toContain("the address join printed for {ip}");
    expect(prompt).toContain("Rules: don't restart or stop any AgentAPI");
    expect(prompt).toContain("Don't print, log or commit the secret.");
  });

  test("one URL: nothing else to try", () => {
    const prompt = joinPrompt(options({hub: {url: "http://hub.vpn:3290/hooks"}, others: []}));
    expect(prompt).toContain("- The hub's webhook URL: http://hub.vpn:3290/hooks.\n");
    expect(prompt).toContain("Then stop and tell me.");
    expect(prompt).not.toContain("try the other URL");
  });

  test("several others: the one the machine reaches", () => {
    const wlan = {url: "http://10.0.0.5:3290/hooks", interface: "wlan0"};
    expect(joinPrompt(options({others: [wlan, vpn]}))).toContain(
      "use the one it reaches of http://10.0.0.5:3290/hooks (wlan0), http://100.116.90.98:3290/hooks (tailscale0) instead",
    );
  });

  test("updates from the hub: --allow-update, the one-time setup and the state DB, each asked first", () => {
    const prompt = joinPrompt(options({allowUpdate: true}));
    expect(prompt).toContain("- Updates from the hub: yes, join with --allow-update");
    expect(prompt).toContain("1. agentapi --version: below 2.2.0, run sudo agentapi update first.");
    expect(prompt).toContain("--public-url 'http://{ip}:{port}' --allow-update\n");
    expect(prompt).toContain("explain the one-time setup it prints");
    expect(prompt).toContain("ask me before you run it");
    expect(prompt).toContain("   state-db: ~/.agentapi/state/{port}.db");
    expect(prompt).toContain("whether the hub can update them");

    const without = joinPrompt(options());
    expect(without).toContain("- Updates from the hub: no, join without --allow-update.");
    expect(without).not.toContain("state-db");
    expect(without).not.toContain(" --allow-update\n");
  });

  test("a secret the hub doesn't show: the agent asks for it", () => {
    const prompt = joinPrompt(options({secret: undefined}));
    expect(prompt).toContain("- The hub secret: ask me for it (the one given to agentapi hub serve).");
    expect(prompt).toContain(`AGENTAPI_HUB_SECRET='${SECRET_PLACEHOLDER}' agentapi hub join`);
  });

  test("masked, the secret never shows", () => {
    for (const allowUpdate of [false, true]) {
      const masked = joinPrompt(options({allowUpdate, masked: true}));
      expect(masked).not.toContain(SECRET);
      expect(masked).toContain(`- The hub secret: \`${SECRET_MASK}\`.`);
      expect(masked).toContain(`AGENTAPI_HUB_SECRET='${SECRET_MASK}'`);
    }
  });
});
