// The commands that add an AgentAPI to a hub (agentapi hub serve, hub join),
// for the chat's "Add to an AgentAPI hub" dialog to copy.

// The placeholder for a hub whose address isn't given.
export const HUB_PLACEHOLDER = "http://<hub>:3290";

// hubAddress is the hub's base URL from what someone typed: a host, a
// host:port, or a URL of the hub (its page or its webhook); empty for none.
export function hubAddress(input: string): string {
  let address = input.trim();
  if (!address) return "";
  if (!/^https?:\/\//i.test(address)) address = `http://${address}`;
  return address.replace(/\/+$/, "").replace(/\/(hooks|chat\/hub)$/, "");
}

// joinedHub is the hub a webhook URL reports to (agentapi hub join points it
// at <hub>/hooks), if it is one.
export function joinedHub(webhookURL: string): string | undefined {
  const match = /^(https?:\/\/.+)\/hooks\/?$/i.exec(webhookURL.trim());
  return match?.[1];
}

// Whether a hub address only works on the hub's own machine.
export function loopback(hub: string): boolean {
  return /^https?:\/\/(localhost|127\.\d+\.\d+\.\d+|\[::1\])(:|\/|$)/i.test(hub);
}

export type JoinCommands = {serve: string; join: string; open: string};

// joinCommands are the steps for the hub at hub (or the placeholder).
export function joinCommands(hub: string): JoinCommands {
  const base = hub || HUB_PLACEHOLDER;
  return {
    serve: [
      "export HUB_SECRET=$(openssl rand -hex 24) HUB_TOKEN=$(openssl rand -hex 16)",
      'echo "HUB_SECRET=$HUB_SECRET HUB_TOKEN=$HUB_TOKEN"',
      'agentapi hub serve --secret "$HUB_SECRET" --token "$HUB_TOKEN"',
    ].join("\n"),
    join: [
      "export HUB_SECRET=<the HUB_SECRET step 1 printed>",
      `agentapi hub join ${base}/hooks --secret "$HUB_SECRET" --public-url 'http://{hostname}:{port}'`,
    ].join("\n"),
    open: `${base}/chat/hub/`,
  };
}
