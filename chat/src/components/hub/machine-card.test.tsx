import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance, Summary} from "@/lib/hub";
import type {HubMachine} from "@/lib/hub-machines";
import {hubInstance, summary as baseSummary} from "@/lib/hub-fixtures";
import type {ListOrder} from "@/lib/hub-order";
import {FixedHubClock} from "./clock";
import {HubListContext} from "./list-context";
import {MachineCard} from "./machine-card";

// One task so far, seen at 9:00.
const summary = (overrides: Partial<Summary> = {}): Summary => baseSummary({tasks: 1, seen_at: "2026-11-01T09:00:00Z", ...overrides});

// An agent on dev-1, in the directory home, running AgentAPI 3.1.0.
const agent = (port: number, agentType: string, overrides: Partial<HubInstance> = {}): HubInstance =>
  hubInstance(`dev-1:${port}`, {
    name: "home",
    hostname: "dev-1",
    agent_type: agentType,
    agentapi_version: "3.1.0",
    last_seen: "2026-11-01T09:59:00Z",
    summary: summary({title: "Nothing to do"}),
    ...overrides,
  });

const claude = agent(3284, "claude", {
  state: "unread",
  summary: summary({
    unread: true,
    title: "Deploy the release",
    spend: {day_usd: 4.2, week_usd: 30, scope: "s1", updated_at: "2026-11-01T09:50:00Z"},
    task: {id: 1, status: "completed", prompt: "Deploy the release", started_at: "2026-11-01T09:30:00Z", ended_at: "2026-11-01T09:40:00Z", errors: [], steered: []},
  }),
});
const codex = agent(3285, "codex", {summary: summary({spend: {day_usd: 1, week_usd: 2, scope: "s2", updated_at: "2026-11-01T09:50:00Z"}})});

const actions = {select() {}, forget() {}, group() {}, newGroup() {}, mute() {}, pin() {}, move() {}};

const card = (instances: HubInstance[], selected?: string, hostname = "dev-1", order: ListOrder = "groups", prefix = "") => {
  const machine: HubMachine = {key: `host ${hostname}`, hostname, instances};
  const section = order === "recent" ? " recent" : "Group";
  return renderToStaticMarkup(
    <FixedHubClock now={Date.parse("2026-11-01T10:00:00Z")}>
      <HubListContext.Provider value={{hubVersion: "3.1.0", root: "", groups: [], prefix, actions}}>
        <MachineCard card={`${section}\nhost ${hostname}`} machine={machine} order={order} selected={selected} />
      </HubListContext.Provider>
    </FixedHubClock>,
  );
};

const text = (html: string) => html.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ");
// The tabs' agents, in order, and the one selected.
const tabs = (html: string) => [...html.matchAll(/role="tab"[^>]*aria-selected="(true|false)"[^>]*data-hub-tab="([^"]+)"/g)].map(([, on, id]) => (on === "true" ? `*${id}` : id));

describe("MachineCard", () => {
  test("the machine once, with what its agents share, a tab per agent, and the first one's panel", () => {
    const html = card([claude, codex]);
    const shown = text(html);
    // The name alone on its line; the tabs (the picked one labelled, the
    // others their marks), then what the agents share; the directory's name,
    // which the machine's doesn't say, in the panel.
    expect(shown).toContain(" dev-1 Claude AgentAPI v3.1.0");
    expect(shown).toContain("Deploy the release home $4.20");
    // The version and the spend of the machine, once: $4.20 and $1.00 in the last 24 hours.
    expect(html.match(/>v3\.1\.0</g)).toHaveLength(1);
    expect(shown).toContain("$5.20 /24h");
    expect(html).toContain('role="tablist" aria-label="Agents on dev-1"');
    expect(tabs(html)).toEqual(["*dev-1:3284", "dev-1:3285"]);
    // One panel: the selected tab's agent, with its menu.
    expect(html).toContain('role="tabpanel"');
    expect(shown).toContain("Finished · Deploy the release home $4.20 in the last 24 hours");
    expect(shown).toContain("$4.20 /24h 20m");
    expect(html).toContain('data-hub-row="dev-1:3284"');
    expect(html).not.toContain('data-hub-row="dev-1:3285"');
    expect(html).toContain('data-hub-row-menu="dev-1:3284"');
    expect(html).not.toContain('data-hub-row-menu="dev-1:3285"');
    expect(html).toContain('aria-label="Claude Code, home, Finished, 20m: Deploy the release"');
    expect(html).toContain('aria-label="Codex, home, Idle"');
    expect(html).not.toContain('aria-current="true"');
  });

  test("only the selected tab is in the tab order, and it labels the panel", () => {
    const html = card([claude, codex]);
    const selected = /<button[^>]*role="tab" id="([^"]+)"[^>]*aria-selected="true"[^>]*tabindex="0"/.exec(html);
    expect(selected).not.toBeNull();
    expect(html).toContain(`aria-labelledby="${selected![1]}"`);
    expect(html.match(/tabindex="-1"/g)).toHaveLength(1);
  });

  test("the tab picked before the page loaded again is the card's tab", () => {
    const items = new Map([["agentapi.hub.machine-tabs", JSON.stringify([["Group\nhost dev-1", "dev-1:3285"]])]]);
    const saved = globalThis.localStorage;
    globalThis.localStorage = {getItem: (key: string) => items.get(key) ?? null} as Storage;
    try {
      expect(tabs(card([claude, codex]))).toEqual(["dev-1:3284", "*dev-1:3285"]);
      // A tab of an agent gone from the machine falls back to the first.
      expect(tabs(card([claude]))).toEqual(["*dev-1:3284"]);
    } finally {
      globalThis.localStorage = saved;
    }
  });

  test("the agent open beside the list is the card's tab, and marked", () => {
    const html = card([claude, codex], "dev-1:3285");
    expect(tabs(html)).toEqual(["dev-1:3284", "*dev-1:3285"]);
    expect(html).toContain('data-hub-row="dev-1:3285"');
    expect(html).toContain('aria-current="true"');
  });

  test("the tabs keep their order (by agent) whatever needs you", () => {
    const asks = agent(3285, "codex", {state: "needs-you", summary: summary({attention: "request", request: {id: "r", kind: "approval", title: "Run it", created_at: "2026-11-01T09:59:00Z"}})});
    expect(tabs(card([asks, claude]))).toEqual(["dev-1:3284", "*dev-1:3285"]);
  });

  test("a name or version the agents don't share shows in the panel", () => {
    const pi = agent(3286, "pi", {name: "site", agentapi_version: "3.0.0"});
    const shown = text(card([pi, claude]));
    expect(shown).not.toContain("dev-1 home");
    expect(shown).toContain("site AgentAPI v3.0.0, older than the hub (v3.1.0) v3.0.0 older");
  });

  test("a name the machine's name says already shows nowhere", () => {
    const shown = text(card([claude, codex], undefined, "coder-home-site"));
    expect(shown).toContain("coder-home-site");
    expect(shown).not.toContain(" home ");
  });

  test("a machine of one agent has one tab, which reads as a label", () => {
    const html = card([codex]);
    expect(tabs(html)).toEqual(["*dev-1:3285"]);
    expect(html).not.toContain("ring-1 ring-border dark:bg-accent");
    // Its spend once, in its panel.
    expect(text(html).match(/\$1\.00 \/24h/g)).toHaveLength(1);
    expect(card([claude, codex])).toContain("ring-1 ring-border dark:bg-accent");
  });

  test("the start most machines share is dimmed, the rest tells it apart", () => {
    const html = card([claude, codex], undefined, "coder-dave-web", "groups", "coder-dave-");
    expect(html).toContain('title="coder-dave-web" data-hub-host="true"><span class="font-normal text-muted-foreground">coder-dave-</span>web</span>');
    // A machine whose name doesn't start so keeps it whole.
    expect(card([claude], undefined, "gpu-9e61", "groups", "coder-dave-")).toContain('data-hub-host="true">gpu-9e61</span>');
  });

  describe("in the recent order", () => {
    // The codex agent changed at 9:58, in the group Ops; Claude Code at 9:41.
    const recentClaude = {...claude, changed_at: "2026-11-01T09:41:00Z"};
    const recentCodex = {...codex, changed_at: "2026-11-01T09:58:00Z", group: "Ops"};

    test("the latest change's agent and its age in the header; the panel's own age for another", () => {
      const html = card([recentCodex, recentClaude], undefined, "dev-1", "recent");
      expect(tabs(html)).toEqual(["dev-1:3284", "*dev-1:3285"]);
      expect(html).toContain('title="The latest change of its agents">2m</span>');
      expect(html).toContain('title="Group: Ops"');
      // Open beside the list: Claude Code's panel, its age that of its latest change (19m).
      const open = card([recentCodex, recentClaude], "dev-1:3284", "dev-1", "recent");
      expect(tabs(open)).toEqual(["*dev-1:3284", "dev-1:3285"]);
      expect(open).toContain('<span class="ml-auto shrink-0 tabular-nums">19m</span>');
    });

    test("a pick from before the latest change gives way to it; a pick after it stays", () => {
      const items = new Map<string, string>();
      const saved = globalThis.localStorage;
      globalThis.localStorage = {getItem: (key: string) => items.get(key) ?? null} as Storage;
      try {
        items.set("agentapi.hub.machine-tabs", JSON.stringify([[" recent\nhost dev-1", "dev-1:3284", Date.parse("2026-11-01T09:50:00Z")]]));
        expect(tabs(card([recentCodex, recentClaude], undefined, "dev-1", "recent"))).toEqual(["dev-1:3284", "*dev-1:3285"]);
        items.set("agentapi.hub.machine-tabs", JSON.stringify([[" recent\nhost dev-1", "dev-1:3284", Date.parse("2026-11-01T09:59:00Z")]]));
        expect(tabs(card([recentCodex, recentClaude], undefined, "dev-1", "recent"))).toEqual(["*dev-1:3284", "dev-1:3285"]);
        // In the groups the pick stays whenever it was made.
        items.set("agentapi.hub.machine-tabs", JSON.stringify([["Group\nhost dev-1", "dev-1:3284", Date.parse("2026-11-01T09:50:00Z")]]));
        expect(tabs(card([recentCodex, recentClaude]))).toEqual(["*dev-1:3284", "dev-1:3285"]);
      } finally {
        globalThis.localStorage = saved;
      }
    });

    test("no header age in the groups", () => {
      expect(card([recentCodex, recentClaude])).not.toContain("The latest change of its agents");
    });
  });
});
