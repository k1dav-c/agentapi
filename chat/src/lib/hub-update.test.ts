import {describe, expect, test} from "bun:test";
import type {HubInstance, InstanceUpdate} from "./hub";
import {hubInstance, summary, update} from "./hub-fixtures";
import {
  cancelRequest,
  cancelUpdate,
  cantUpdateText,
  confirmText,
  olderUpdates,
  requestUpdate,
  requestUpdates,
  resultsText,
  setupCommand,
  shownUpdate,
  updateAction,
  updateBadge,
  updateRequest,
  updateURL,
  type Fetcher,
} from "./hub-update";

// An instance on host that runs version; its summary's update as given,
// none (null) for an AgentAPI from before updates through the API.
const instance = (id: string, given: Partial<InstanceUpdate> | null, overrides: Partial<HubInstance> & {version?: string} = {}): HubInstance => {
  const {version = "2.0.0", ...rest} = overrides;
  const reported = summary({
    instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: version},
    ...(given ? {update: update(given)} : {}),
  });
  return hubInstance(id, {server_id: "s", hostname: "dev-1", agentapi_version: version, summary: reported, ...rest});
};

const BLOCKED =
  "can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable; once: mkdir -p ~/.local/bin && cp /usr/local/bin/agentapi ~/.local/bin/agentapi && sudo ln -sf ~/.local/bin/agentapi /usr/local/bin/agentapi";
const COMMAND = "mkdir -p ~/.local/bin && cp /usr/local/bin/agentapi ~/.local/bin/agentapi && sudo ln -sf ~/.local/bin/agentapi /usr/local/bin/agentapi";

describe("the row's menu", () => {
  test("an AgentAPI from before updates through the API offers nothing new", () => {
    expect(updateAction(instance("a", null))).toBeUndefined();
    expect(updateAction(instance("a", null, {summary: null}))).toBeUndefined();
  });

  test("updates off on its machine: why, in place of the update", () => {
    expect(updateAction(instance("a", {allowed: false}))).toEqual({kind: "off"});
    // Off wins over anything else it reports.
    expect(updateAction(instance("a", {allowed: false, blocked: BLOCKED}))).toEqual({kind: "off"});
  });

  test("blocked: the one-time setup and its command, in place of the update", () => {
    expect(updateAction(instance("a", {blocked: BLOCKED}))).toEqual({kind: "blocked", reason: "can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable", command: COMMAND});
  });

  test("allowed and not blocked: the update, again after one failed", () => {
    expect(updateAction(instance("a", {}))).toEqual({kind: "update"});
    expect(updateAction(instance("a", {state: "failed", error: "checksums.txt doesn't list agentapi-linux-amd64"}))).toEqual({
      kind: "update",
      failed: "checksums.txt doesn't list agentapi-linux-amd64",
    });
    expect(updateAction(instance("a", {state: "failed"}))).toEqual({kind: "update", failed: "It didn't say why."});
  });

  test("one that waits for the agent can be cancelled; one under way can't be started again", () => {
    expect(updateAction(instance("a", {state: "waiting", target: "2.1.0"}))?.kind).toBe("cancel");
    for (const state of ["checking", "downloading", "restarting", "verifying"]) expect(updateAction(instance("a", {state}))?.kind).toBe("busy");
  });

  test("nothing for an instance the hub doesn't follow now, or a summary of an earlier run", () => {
    for (const connection of ["stopped", "unreachable", "connecting"] as const) {
      expect(updateAction(instance("a", {}, {connection, state: connection}))).toBeUndefined();
    }
    // Restarted: the new run's event came, its first summary not yet.
    expect(updateAction(instance("a", {state: "restarting"}, {server_id: "s2"}))).toBeUndefined();
  });
});

describe("the setup command in blocked", () => {
  test("the command in backquotes, the rest is the reason", () => {
    expect(setupCommand(`/usr/local/bin isn't writable. Run once: \`${COMMAND}\`.`)).toEqual({reason: "/usr/local/bin isn't writable.", command: COMMAND});
  });

  // The text lib/selfupdate's Program.Blocked writes, with a home of /home/me.
  test("what an AgentAPI sends", () => {
    const command = "mkdir -p /home/me/.local/bin && cp /usr/local/bin/agentapi /home/me/.local/bin/agentapi && sudo ln -sf /home/me/.local/bin/agentapi /usr/local/bin/agentapi";
    expect(setupCommand(`can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable. Move it to a directory you own, once: ${command}`)).toEqual({
      reason: "can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable. Move it to a directory you own, once",
      command,
    });
    // A copy in ~/.local/bin that can't be replaced either: no command to offer.
    expect(setupCommand("can't replace /home/me/.local/bin/agentapi (which /usr/local/bin/agentapi links to): /home/me/.local/bin isn't writable")).toEqual({
      reason: "can't replace /home/me/.local/bin/agentapi (which /usr/local/bin/agentapi links to): /home/me/.local/bin isn't writable",
    });
  });

  test("else from its mkdir -p to the end of its sentence", () => {
    expect(setupCommand(`${BLOCKED}.`)).toEqual({reason: "can't replace /usr/local/bin/agentapi: /usr/local/bin isn't writable", command: COMMAND});
    expect(setupCommand(`Fix it once: ${COMMAND}. Then update again.`)).toEqual({reason: "Then update again.", command: COMMAND});
    // A dot in a path ends no sentence, and a long clause before the colon is the reason.
    expect(setupCommand(`/home/me/.local/bin isn't writable: ${COMMAND}`)).toEqual({reason: "/home/me/.local/bin isn't writable", command: COMMAND});
    expect(setupCommand(`AgentAPI can't replace /opt/bin/agentapi, a directory it can't write: ${COMMAND}`)).toEqual({
      reason: "AgentAPI can't replace /opt/bin/agentapi, a directory it can't write",
      command: COMMAND,
    });
  });

  test("a reason with no command is all reason", () => {
    expect(setupCommand(" a development build can't update itself ")).toEqual({reason: "a development build can't update itself"});
    // A path in backquotes isn't the command.
    expect(setupCommand("`/opt/agentapi` is read-only")).toEqual({reason: "`/opt/agentapi` is read-only"});
  });
});

describe("the chip", () => {
  test("shows an update under way or one that failed, of the run the hub follows", () => {
    expect(shownUpdate(instance("a", null))).toBeUndefined();
    expect(shownUpdate(instance("a", {}))).toBeUndefined();
    expect(shownUpdate(instance("a", {state: "waiting"}))?.state).toBe("waiting");
    expect(shownUpdate(instance("a", {state: "failed", error: "x"}))?.state).toBe("failed");
    // Restarting: the hub reconnects meanwhile.
    expect(shownUpdate(instance("a", {state: "restarting"}, {connection: "connecting", state: "connecting"}))?.state).toBe("restarting");
    expect(shownUpdate(instance("a", {state: "waiting"}, {connection: "connecting", state: "connecting"}))).toBeUndefined();
    expect(shownUpdate(instance("a", {state: "failed"}, {connection: "stopped", state: "stopped"}))).toBeUndefined();
    // The new run's event came: the restart is done.
    expect(shownUpdate(instance("a", {state: "restarting"}, {server_id: "s2", connection: "connecting", state: "connecting"}))).toBeUndefined();
  });

  test("labels: short, with the whole of it in the title", () => {
    const labels = (given: Partial<InstanceUpdate>) => {
      const badge = updateBadge(update(given));
      return [badge.label, badge.short, badge.tone];
    };
    expect(labels({state: "checking"})).toEqual(["Checking for update", "Updating", "working"]);
    expect(labels({state: "downloading", target: "2.1.0"})).toEqual(["Downloading v2.1.0", "Updating", "working"]);
    expect(labels({state: "downloading"})).toEqual(["Downloading update", "Updating", "working"]);
    expect(labels({state: "waiting", target: "2.1.0"})).toEqual(["Update waits for the task", "Update waits", "waiting"]);
    expect(labels({state: "restarting", target: "2.1.0"})).toEqual(["Restarting into v2.1.0", "Restarting", "working"]);
    expect(labels({state: "restarting"})).toEqual(["Restarting", "Restarting", "working"]);
    expect(labels({state: "failed", error: "no network"})).toEqual(["Update failed", "Update failed", "fault"]);
    expect(labels({state: "verifying"})).toEqual(["Updating", "Updating", "working"]);
    expect(updateBadge(update({state: "failed", error: "no network"})).title).toBe("The AgentAPI update failed: no network");
    expect(updateBadge(update({state: "waiting", target: "2.1.0"})).title).toBe("Updating AgentAPI to v2.1.0 once the agent is idle: its task finished, nothing waiting for an answer");
  });
});

// A fetcher that answers each request with the next response and keeps
// what it was asked.
function fetcher(...responses: (Response | Error)[]): Fetcher & {calls: {url: string; init: RequestInit}[]} {
  const calls: {url: string; init: RequestInit}[] = [];
  const fake = async (url: string, init: RequestInit) => {
    calls.push({url, init});
    const next = responses.shift() ?? new Response("{}");
    if (next instanceof Error) throw next;
    return next;
  };
  return Object.assign(fake, {calls});
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), {status, headers: {"Content-Type": "application/json"}});

describe("requests", () => {
  test("go to the instance's /update through the hub's proxy", () => {
    expect(updateURL("https://hub.vpn:3290", "dev-1:3284")).toBe("https://hub.vpn:3290/i/dev-1%3A3284/update");
    expect(updateURL("https://coder.example.com/apps/hub", "a/b")).toBe("https://coder.example.com/apps/hub/i/a%2Fb/update");
    expect(updateRequest("idle")).toEqual({method: "POST", credentials: "same-origin", headers: {"Content-Type": "application/json"}, body: '{"when":"idle"}'});
    expect(updateRequest("now").body).toBe('{"when":"now"}');
    expect(cancelRequest()).toEqual({method: "DELETE", credentials: "same-origin"});
  });

  test("the answer: the update's state, or the instance's or the hub's error", async () => {
    const answers = fetcher(
      json(202, {state: "waiting", target: "2.1.0"}),
      json(200, {state: "idle", up_to_date: true}),
      json(403, {title: "Forbidden", status: 403, detail: "updates through the API are off: set allow-update: true in ~/.agentapi/config.yaml"}),
      json(409, {title: "Conflict", status: 409, detail: BLOCKED}),
      json(502, {error: "the instance is unreachable: connection refused"}),
      new Response("404 page not found", {status: 404}),
      new TypeError("Failed to fetch"),
    );
    const ask = () => requestUpdate("", "a", "idle", answers);
    expect(await ask()).toEqual({ok: true, state: "waiting", target: "2.1.0", upToDate: false});
    expect(await ask()).toEqual({ok: true, state: "idle", target: undefined, upToDate: true});
    expect(await ask()).toEqual({ok: false, status: 403, error: "updates through the API are off: set allow-update: true in ~/.agentapi/config.yaml"});
    expect(await ask()).toEqual({ok: false, status: 409, error: BLOCKED});
    expect(await ask()).toEqual({ok: false, status: 502, error: "the instance is unreachable: connection refused"});
    expect(await ask()).toEqual({ok: false, status: 404, error: "It answered 404."});
    expect(await ask()).toEqual({ok: false, status: 0, error: "The hub didn't answer."});
    expect(answers.calls[0]).toEqual({url: "/i/a/update", init: updateRequest("idle")});
  });

  test("cancelling deletes the update; one that restarts already can't be", async () => {
    const answers = fetcher(json(200, {state: "idle"}), json(409, {detail: "the update is restarting the server already"}));
    expect(await cancelUpdate("https://hub.vpn:3290", "dev-1:3284", answers)).toMatchObject({ok: true, state: "idle"});
    expect(await cancelUpdate("https://hub.vpn:3290", "dev-1:3284", answers)).toEqual({ok: false, status: 409, error: "the update is restarting the server already"});
    expect(answers.calls[0]).toEqual({url: "https://hub.vpn:3290/i/dev-1%3A3284/update", init: {method: "DELETE", credentials: "same-origin"}});
  });
});

describe("update the older ones", () => {
  const rows = [
    instance("ready", {}, {hostname: "dev-1"}),
    instance("failed", {state: "failed", error: "x"}, {hostname: "dev-1"}),
    instance("fresh", {keeps_conversation: false}, {hostname: "dev-2"}),
    instance("waits", {state: "waiting"}, {hostname: "dev-2"}),
    instance("downloads", {state: "downloading"}, {hostname: "dev-2"}),
    instance("blocked", {blocked: BLOCKED}, {hostname: "dev-3"}),
    instance("off", {allowed: false}, {hostname: "dev-4"}),
    instance("off-too", {allowed: false}, {hostname: "dev-5"}),
    instance("old", null, {hostname: "dev-6", version: "1.6.1"}),
    // Not older than the hub, or not followed now: not counted at all.
    instance("same", {}, {version: "2.1.0"}),
    instance("newer", {}, {version: "2.2.0"}),
    instance("dev", {}, {version: "0.0.0-dev"}),
    instance("stopped", {}, {connection: "stopped", state: "stopped"}),
  ];
  const ids = (instances: HubInstance[]) => instances.map((i) => i.id);

  test("targets the connected older ones that can update now, and sorts out the others", () => {
    const older = olderUpdates(rows, "2.1.0");
    expect(ids(older.targets)).toEqual(["ready", "failed", "fresh"]);
    expect(ids(older.updating)).toEqual(["waits", "downloads"]);
    expect(ids(older.blocked)).toEqual(["blocked"]);
    expect(ids(older.off)).toEqual(["off", "off-too"]);
    expect(ids(older.unsupported)).toEqual(["old"]);
    // A development build of the hub tells none older.
    expect(Object.values(olderUpdates(rows, "0.0.0-dev")).flat()).toEqual([]);
  });

  test("says which it can't update, and why", () => {
    expect(cantUpdateText(olderUpdates(rows, "2.1.0"))).toBe(
      "Can't update from here: 1 needs a one-time setup (dev-3); 2 have updates from the hub off (dev-4, dev-5); 1 runs an AgentAPI from before updates from the hub. The ⋯ menu of each agent says how to fix its machine.",
    );
    expect(cantUpdateText(olderUpdates([instance("old", null)], "2.1.0"))).toBe("Can't update from here: 1 runs an AgentAPI from before updates from the hub.");
    expect(cantUpdateText(olderUpdates(rows.slice(0, 3), "2.1.0"))).toBe("");
  });

  test("the confirm step says which start a fresh conversation", () => {
    const targets = olderUpdates(rows, "2.1.0").targets;
    expect(confirmText(targets)).toBe("Update 3 agents once each one's task ends? 1 runs without a state DB, so its conversation starts fresh after the restart.");
    expect(confirmText(targets.slice(0, 1))).toBe("Update 1 agent once each one's task ends?");
  });

  test("asks every target at once to update when idle, and says what they answered", async () => {
    const targets = olderUpdates(rows, "2.1.0").targets;
    // Each answer waits until all three were asked.
    let release!: () => void;
    const asked = new Promise<void>((resolve) => (release = resolve));
    const calls: {url: string; init: RequestInit}[] = [];
    const answers = [json(202, {state: "waiting", target: "2.1.0"}), json(200, {state: "idle", up_to_date: true}), json(409, {detail: "the server is stopping"})];
    const slow: Fetcher = async (url, init) => {
      const answer = answers[calls.length];
      calls.push({url, init});
      if (calls.length === targets.length) release();
      await asked;
      return answer;
    };
    const results = await requestUpdates("https://hub.vpn:3290", targets, slow);
    expect(calls.map((call) => call.url)).toEqual(["https://hub.vpn:3290/i/ready/update", "https://hub.vpn:3290/i/failed/update", "https://hub.vpn:3290/i/fresh/update"]);
    expect(calls.every((call) => call.init.method === "POST" && call.init.body === '{"when":"idle"}')).toBe(true);
    expect(ids(results.asked)).toEqual(["ready"]);
    expect(ids(results.upToDate)).toEqual(["failed"]);
    expect(results.refused.map(({instance, error}) => [instance.id, error])).toEqual([["fresh", "the server is stopping"]]);
    expect(resultsText(results)).toBe("Asked 1 agent to update; each restarts once its task ends. 1 runs the latest release already. 1 refused.");
    expect(resultsText({asked: targets, upToDate: [], refused: []})).toBe("Asked 3 agents to update; each restarts once its task ends.");
  });
});
