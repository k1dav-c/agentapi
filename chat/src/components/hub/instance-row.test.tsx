import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance, Summary} from "@/lib/hub";
import {hubInstance, summary} from "@/lib/hub-fixtures";
import type {ListOrder} from "@/lib/hub-order";
import {FixedHubClock} from "./clock";
import {InstanceRow, agentName} from "./instance-row";
import {HubListContext, pinPlace} from "./list-context";

// Finished at 9:40, which the hub saw at 9:41; in the group Client work.
const finished: HubInstance = hubInstance("dev-1:3284", {
  name: "checkout-api",
  hostname: "dev-1",
  agent_type: "claude",
  last_seen: "2026-11-01T09:59:00Z",
  changed_at: "2026-11-01T09:41:00Z",
  group: "Client work",
  state: "unread",
  summary: summary({
    title: "Deploy the release",
    tasks: 1,
    unread: true,
    seen_at: "2026-11-01T09:00:00Z",
    task: {id: 1, status: "completed", prompt: "Deploy the release", started_at: "2026-11-01T09:30:00Z", ended_at: "2026-11-01T09:40:00Z", errors: [], steered: []},
  }),
});

const actions = {select() {}, forget() {}, group() {}, newGroup() {}, mute() {}, pin() {}, move() {}};

const row = (order: ListOrder, target: HubInstance = finished, pinned?: {first: boolean; last: boolean}) =>
  renderToStaticMarkup(
    <FixedHubClock now={Date.parse("2026-11-01T10:00:00Z")}>
      <HubListContext.Provider value={{hubVersion: "2.1.0", root: "", groups: ["Client work"], prefix: "", actions}}>
        <InstanceRow instance={target} order={order} selected={false} pinned={pinned && pinPlace(pinned.first, pinned.last)} />
      </HubListContext.Provider>
    </FixedHubClock>,
  );

describe("InstanceRow in the list's orders", () => {
  test("groups: the age of what it does, and no group of its own (its header names it)", () => {
    const html = row("groups");
    expect(html).toContain(">20m</span>");
    expect(html).not.toContain("Group: Client work");
  });

  test("recent: the age of its latest change, and its group", () => {
    const html = row("recent");
    expect(html).toContain(">19m</span>");
    expect(html).toContain('title="Group: Client work"');
    expect(html).toContain(">Client work</span>");
    expect(html).toContain("checkout-api, Finished, 19m: Deploy the release. Group: Client work");
  });

  test("recent: an instance in no group names none", () => {
    const html = row("recent", {...finished, group: undefined});
    expect(html).not.toContain("Group:");
  });
});

describe("InstanceRow among the pinned ones", () => {
  const top = {first: true, last: false};

  test("groups: its group and a pin, the age of what it does", () => {
    const html = row("groups", {...finished, pin: 1}, top);
    expect(html).toContain('title="Group: Client work"');
    expect(html).toContain("lucide-pin");
    expect(html).toContain(">20m</span>");
    expect(html).toContain("checkout-api, Finished, 20m: Deploy the release. Pinned. Group: Client work");
  });

  test("recent: the age of its latest change", () => {
    const html = row("recent", {...finished, pin: 1}, top);
    expect(html).toContain("checkout-api, Finished, 19m: Deploy the release. Pinned. Group: Client work");
  });

  test("in no group: a pin, no group", () => {
    const html = row("groups", {...finished, group: undefined, pin: 1}, top);
    expect(html).toContain("lucide-pin");
    expect(html).not.toContain("Group:");
  });

  test("a row that isn't pinned has no pin", () => {
    const html = row("groups");
    expect(html).not.toContain("lucide-pin");
    expect(html).not.toContain("Pinned");
  });
});

describe("InstanceRow's work time", () => {
  test("the last 24 hours, with the span going on, while it worked this week", () => {
    const working: HubInstance = {...finished, state: "working", work: {last_24h_seconds: 1800, last_7d_seconds: 7200, working_since: "2026-11-01T09:30:00Z"}};
    const html = row("groups", working);
    expect(html).toContain("Worked 1h in the last 24 hours, 2.5h in the last 7 days (as the hub saw it)");
    expect(html).toContain('<span aria-hidden="true">1h</span>');
    expect(row("groups")).not.toContain("data-hub-work");
    expect(row("groups", {...finished, work: {last_24h_seconds: 0, last_7d_seconds: 0}})).not.toContain("data-hub-work");
    // A usage limit's bar takes the room.
    const limited = {...working, summary: summary({...finished.summary, usage: {rate_limits: [{id: "five_hour", label: "5h", used_percent: 80}]}})};
    const limitedRow = row("groups", limited);
    expect(limitedRow).toContain("Usage limit 80% used");
    expect(limitedRow).not.toContain("data-hub-work");
  });
});

describe("InstanceRow's spend", () => {
  const spend = {day_usd: 4.2, week_usd: 30, scope: "s1", updated_at: "2026-11-01T09:59:00Z"};
  const spending = (s: Summary["spend"]): HubInstance => ({...finished, summary: summary({...finished.summary, spend: s})});

  test("its title and screen reader text say the tokens of the last 24 hours", () => {
    const tokens = {input: 1_000, output: 400_000, cache_write: 200_000, cache_read: 2_800_000, total: 3_401_000};
    const html = row("groups", spending({...spend, day_tokens: tokens, week_tokens: tokens}));
    const title = "$4.20 in the last 24 hours, $30 in the last 7 days (API-equivalent, at list prices; a subscription doesn&#x27;t charge it); 3.4M tokens in the last 24 hours";
    expect(html).toContain(`title="${title}"`);
    expect(html).toContain(`<span class="sr-only">${title}</span>`);
    expect(html).toContain(">$4.20<");
    expect(html).not.toContain("3.4M<");
  });

  test("from an AgentAPI that doesn't say its tokens, none", () => {
    expect(row("groups", spending(spend))).not.toContain("tokens");
  });
});

describe("agentName", () => {
  test("names the agents AgentAPI 3 runs, and an older AgentAPI's others by their type", () => {
    expect(agentName(finished)).toBe("Claude Code");
    expect(agentName({...finished, agent_type: "aider"})).toBe("aider");
    expect(agentName({...finished, agent_type: "custom"})).toBe("Custom");
    expect(agentName({...finished, agent_type: ""})).toBe("");
  });
});
