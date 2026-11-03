import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance, InstanceUpdate, Summary} from "@/lib/hub";
import {FixedHubClock} from "./clock";
import {VersionsCard, WorkCard} from "./overview";

const running = (id: string, version: string, hostname: string, overrides: Partial<HubInstance> = {}): HubInstance => ({
  id,
  url: `http://${id}`,
  server_id: "s",
  hostname,
  agentapi_version: version,
  source: "webhook",
  last_seen: "2026-11-01T00:00:00Z",
  connection: "connected",
  state: "idle",
  ...overrides,
});

// The text of html, without its tags; a machine's name, in an element of
// its own (data-hub-host), reads as part of its sentence.
const text = (html: string) =>
  html
    .replace(/<span data-hub-host(="[^"]*")?>([^<]*)<\/span>/g, "$2")
    .replace(/<[^>]+>/g, " ").replaceAll("&#x27;", "'").replace(/\s+/g, " ");

describe("VersionsCard", () => {
  const instances = [
    running("a", "1.6.1", "dev-2"),
    running("b", "1.6.1", "dev-1"),
    running("c", "2.1.0", "dev-3"),
    running("d", "2.0.0", "dev-3"),
    running("e", "1.2.0", "dev-4", {connection: "stopped", state: "stopped"}),
  ];

  test("counts each version and those older than the hub, and says how to update them", () => {
    const html = renderToStaticMarkup(<VersionsCard root="" instances={instances} hubVersion="2.1.0" />);
    const shown = text(html);
    expect(shown).toContain("the hub runs v2.1.0");
    expect(shown).toContain("Older than the hub 3 of 4 connected agents");
    expect(shown).toContain("v2.1.0 the hub's dev-3 1 v2.0.0 older dev-3 1 v1.6.1 older dev-1, dev-2 2");
    expect(shown).not.toContain("v1.2.0");
    expect(shown).toContain("sudo agentapi update --restart");
    expect(shown).toContain("before v2.1.0, update, then restart each AgentAPI once");
  });

  test("with every agent on the hub's version, there is nothing to update", () => {
    const html = renderToStaticMarkup(<VersionsCard root="" instances={[running("c", "2.1.0", "dev-3")]} hubVersion="2.1.0" />);
    expect(text(html)).toContain("Older than the hub 0 of 1 connected agent");
    expect(html).not.toContain("sudo agentapi update");
  });

  test("a development build of the hub tells no version older", () => {
    const html = renderToStaticMarkup(<VersionsCard root="" instances={instances} hubVersion="0.0.0-dev" />);
    expect(text(html)).toContain("The hub is a development build");
    expect(html).not.toContain("Older than the hub");
    expect(html).not.toContain(">older</span>");
    expect(html).not.toContain("sudo agentapi update");
  });

  test("nothing to show without a connected agent", () => {
    expect(renderToStaticMarkup(<VersionsCard root="" instances={[instances[4]]} hubVersion="2.1.0" />)).toBe("");
  });

  // An instance whose summary says how it updates through its API.
  const updating = (id: string, version: string, hostname: string, update: Partial<InstanceUpdate>): HubInstance =>
    running(id, version, hostname, {
      summary: {
        instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: version},
        status: "stable",
        lifecycle: "ready",
        attention: "none",
        title: "",
        pending_requests: 0,
        queued_messages: 0,
        tasks: 0,
        unread: false,
        seen_at: "2026-11-01T00:00:00Z",
        update: {allowed: true, state: "idle", keeps_conversation: true, ...update},
      } as Summary,
    });

  test("the older agents it can update get a button; the others, why, and how to update them by hand", () => {
    const html = renderToStaticMarkup(
      <VersionsCard
        root=""
        instances={[
          updating("a", "2.0.0", "dev-1", {}),
          updating("b", "2.0.0", "dev-2", {blocked: "can't replace /usr/local/bin/agentapi"}),
          updating("c", "2.0.0", "dev-3", {allowed: false}),
          running("d", "1.6.1", "dev-4"),
          updating("e", "2.1.0", "dev-5", {}),
        ]}
        hubVersion="2.1.0"
      />,
    );
    const shown = text(html);
    expect(shown).toContain("Update the older ones");
    expect(shown).toContain("1 of 4 can update from here; each waits for its task to finish.");
    expect(shown).toContain(
      "Can't update from here: 1 needs a one-time setup (dev-2); 1 has updates from the hub off (dev-3); 1 runs an AgentAPI from before updates from the hub.",
    );
    expect(shown).toContain("sudo agentapi update --restart");
  });

  test("with every older agent updating from here, no hint", () => {
    const html = renderToStaticMarkup(<VersionsCard root="" instances={[updating("a", "2.0.0", "dev-1", {}), updating("b", "1.6.1", "dev-1", {state: "failed"})]} hubVersion="2.1.0" />);
    expect(text(html)).toContain("2 of 2 can update from here");
    expect(html).not.toContain("Can't update from here");
    expect(html).not.toContain("sudo agentapi update");
  });

  test("updates under way count, without a button once none is left to ask", () => {
    const html = renderToStaticMarkup(
      <VersionsCard root="" instances={[updating("a", "2.0.0", "dev-1", {state: "waiting"}), updating("b", "2.0.0", "dev-1", {state: "downloading"})]} hubVersion="2.1.0" />,
    );
    expect(text(html)).toContain("2 updating now.");
    expect(html).not.toContain("Update the older ones");
  });
});

describe("WorkCard", () => {
  const now = Date.parse("2026-11-03T12:00:00Z");
  const card = (instances: HubInstance[]) =>
    renderToStaticMarkup(
      <FixedHubClock now={now}>
        <WorkCard instances={instances} onOpen={() => {}} />
      </FixedHubClock>,
    );

  test("totals, the agents that worked the most and each group, with the span going on", () => {
    const shown = text(
      card([
        running("a", "2.5.0", "dev-1", {name: "checkout-api", group: "Client work", work: {last_24h_seconds: 1800, last_7d_seconds: 4 * 3600}}),
        running("b", "2.5.0", "dev-2", {name: "billing", state: "working", work: {last_24h_seconds: 0, last_7d_seconds: 3600, working_since: "2026-11-03T11:00:00Z"}}),
        running("c", "2.5.0", "dev-3", {name: "docs", connection: "stopped", state: "stopped", group: "Client work", work: {last_24h_seconds: 0, last_7d_seconds: 600}}),
        running("d", "2.4.0", "dev-4", {name: "older hub"}),
      ]),
    );
    expect(shown).toContain("Last 24 hours 1.5h Last 7 days 6.1h");
    expect(shown).toContain("3 of 4 agents worked this week");
    expect(shown).toContain("Worked the most this week 24 hours 7 days checkout-api 30m 4h billing 1h 2h docs 0m 10m");
    expect(shown).toContain("By group 24 hours 7 days Client work 30m 4.1h Ungrouped 1h 2h");
  });

  test("nothing without work time, and no groups when none is named", () => {
    expect(card([running("a", "2.4.0", "dev-1")])).toBe("");
    const shown = text(card([running("a", "2.5.0", "dev-1", {work: {last_24h_seconds: 60, last_7d_seconds: 60}})]));
    expect(shown).not.toContain("By group");
  });
});
