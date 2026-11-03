import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance, Summary} from "@/lib/hub";
import type {ListOrder} from "@/lib/hub-order";
import {FixedHubClock} from "./clock";
import {InstanceRow} from "./instance-row";

// Finished at 9:40, which the hub saw at 9:41; in the group Client work.
const finished: HubInstance = {
  id: "dev-1:3284",
  name: "checkout-api",
  url: "http://dev-1:3284",
  hostname: "dev-1",
  agent_type: "claude",
  source: "webhook",
  last_seen: "2026-11-01T09:59:00Z",
  changed_at: "2026-11-01T09:41:00Z",
  connection: "connected",
  group: "Client work",
  state: "unread",
  summary: {
    instance: {id: "dev-1:3284", server_id: "s", agent_type: "claude", transport: "native"},
    status: "stable",
    lifecycle: "ready",
    attention: "none",
    title: "Deploy the release",
    pending_requests: 0,
    queued_messages: 0,
    tasks: 1,
    unread: true,
    seen_at: "2026-11-01T09:00:00Z",
    task: {id: 1, status: "completed", prompt: "Deploy the release", started_at: "2026-11-01T09:30:00Z", ended_at: "2026-11-01T09:40:00Z", errors: [], steered: []},
  } as Summary,
};

const actions = {select() {}, forget() {}, group() {}, newGroup() {}, mute() {}, pin() {}, move() {}};

const row = (order: ListOrder, target: HubInstance = finished, pinned?: {first: boolean; last: boolean}) =>
  renderToStaticMarkup(
    <FixedHubClock now={Date.parse("2026-11-01T10:00:00Z")}>
      <InstanceRow instance={target} hubVersion="2.1.0" root="" groups={["Client work"]} order={order} selected={false} pinned={pinned} actions={actions} />
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
