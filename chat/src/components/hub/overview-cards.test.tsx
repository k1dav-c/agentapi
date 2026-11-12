import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance, InstanceUpdate} from "@/lib/hub";
import {hubInstance, summary, update} from "@/lib/hub-fixtures";
import {FixedHubClock} from "./clock";
import {ProvidersCard, SpendCard, TokensCard, VersionsCard, WorkCard} from "./overview-cards";

const running = (id: string, version: string, hostname: string, overrides: Partial<HubInstance> = {}): HubInstance =>
  hubInstance(id, {server_id: "s", hostname, agentapi_version: version, ...overrides});

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
  const updating = (id: string, version: string, hostname: string, given: Partial<InstanceUpdate>): HubInstance =>
    running(id, version, hostname, {
      summary: summary({
        instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: version},
        update: update(given),
      }),
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

  test("totals, the agents that worked the most with their machines, each group and each machine, with the span going on", () => {
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
    expect(shown).toContain("Worked the most this week 24 hours 7 days checkout-api dev-1 30m 4h billing dev-2 1h 2h docs dev-3 0m 10m");
    expect(shown).toContain("By group 24 hours 7 days Client work 30m 4.1h Ungrouped 1h 2h");
    expect(shown).toContain("By machine 24 hours 7 days dev-1 30m 4h dev-2 1h 2h dev-3 0m 10m");
  });

  test("nothing without work time, and no groups when none is named, no machines on one", () => {
    expect(card([running("a", "2.4.0", "dev-1")])).toBe("");
    const shown = text(
      card([running("a", "2.5.0", "dev-1", {work: {last_24h_seconds: 60, last_7d_seconds: 60}}), running("b", "2.5.0", "dev-1", {work: {last_24h_seconds: 60, last_7d_seconds: 60}})]),
    );
    expect(shown).not.toContain("By group");
    expect(shown).not.toContain("By machine");
  });

  test("the machines past the eighth add up in one", () => {
    const machines = Array.from({length: 10}, (_, i) => running(`m${i}`, "2.5.0", `dev-${i}`, {work: {last_24h_seconds: 0, last_7d_seconds: (10 - i) * 3600}}));
    const shown = text(card(machines));
    expect(shown).toContain("dev-6 0m 4h Other 3 machines 0m 6h");
    expect(shown).not.toContain("dev-7 ");
  });
});

describe("TokensCard", () => {
  const tokens = (n: number) => ({input: n, output: 2 * n, cache_write: 3 * n, cache_read: 4 * n, total: 10 * n});
  // An instance whose spend says n tokens of each kind today, 10n this week.
  const using = (id: string, n: number, scope: string, overrides: Partial<HubInstance> = {}, updated_at = "2026-11-03T11:00:00Z"): HubInstance =>
    running(id, "2.4.0", "dev-1", {
      summary: summary({
        instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: "2.4.0"},
        spend: {day_usd: n, week_usd: 10 * n, scope, updated_at, day_tokens: tokens(n), week_tokens: tokens(10 * n)},
      }),
      ...overrides,
    });
  const card = (instances: HubInstance[]) => text(renderToStaticMarkup(<TokensCard instances={instances} onOpen={() => {}} />));

  test("totals, each kind, the agents that used the most and each group, each scope once and its machine", () => {
    const shown = card([
      using("a", 120_000, "s1", {name: "checkout-api", group: "Client work"}),
      // The same sessions as a, figured a minute earlier: not counted.
      using("b", 99_000, "s1", {name: "checkout-api again", group: "Client work"}, "2026-11-03T10:59:00Z"),
      using("c", 30_000, "s2", {name: "billing"}),
      using("d", 1_000_000, "s3", {name: "stopped", connection: "stopped", state: "stopped"}),
      running("e", "2.3.0", "dev-2", {name: "older"}),
    ]);
    expect(shown).toContain("Last 24 hours 1.5M Last 7 days 15M");
    expect(shown).toContain("3 of 5 agents report it");
    expect(shown).toContain("By kind 24 hours 7 days Input 150k 1.5M Output 300k 3M Cache write 450k 4.5M Cache read 600k 6M");
    expect(shown).toContain("Used the most this week 24 hours 7 days checkout-api dev-1 1.2M 12M billing dev-1 300k 3M");
    expect(shown).not.toContain("checkout-api again");
    expect(shown).toContain("By group 24 hours 7 days Client work 1.2M 12M Ungrouped 300k 3M");
    // All on dev-1: the machine would be the total.
    expect(shown).not.toContain("By machine");
  });

  test("by machine: each scope once, as the totals count them", () => {
    const shown = card([
      using("a", 120_000, "s1", {name: "checkout-api"}),
      using("b", 99_000, "s1", {name: "checkout-api"}, "2026-11-03T10:59:00Z"),
      using("c", 30_000, "s2", {name: "billing", hostname: "dev-2"}),
      using("d", 10_000, "s3", {name: "docs", hostname: undefined}),
    ]);
    expect(shown).toContain("By machine 24 hours 7 days dev-1 1.2M 12M dev-2 300k 3M Not reported 100k 1M");
  });

  test("nothing without tokens, and no groups when none is named", () => {
    expect(card([running("e", "2.3.0", "dev-2")])).toBe("");
    expect(card([using("a", 0, "s1")])).toBe("");
    expect(card([using("a", 5, "s1")])).not.toContain("By group");
  });
});

describe("SpendCard", () => {
  const spending = (id: string, hostname: string, week: number, scope: string, overrides: Partial<HubInstance> = {}): HubInstance =>
    running(id, "2.4.0", hostname, {
      agent_type: "claude",
      summary: summary({
        instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: "2.4.0"},
        spend: {day_usd: week / 10, week_usd: week, scope, updated_at: "2026-11-03T11:00:00Z"},
      }),
      ...overrides,
    });
  const card = (instances: HubInstance[]) => text(renderToStaticMarkup(<SpendCard instances={instances} onOpen={() => {}} />));

  test("the agents that spent the most, each with its machine, and each machine", () => {
    const shown = card([
      spending("a", "dev-1", 40, "s1", {name: "home"}),
      spending("b", "dev-1", 20, "s2", {name: "home", agent_type: "codex"}),
      spending("c", "dev-2", 50, "s3", {name: "home"}),
      // Named after its machine: the machine once.
      spending("d", "dev-3", 5, "s4", {name: "dev-3"}),
    ]);
    expect(shown).toContain("Last 24 hours $12 Last 7 days $115");
    expect(shown).toContain("Spent the most this week 24 hours 7 days home dev-2 $5.00 $50 home dev-1 $4.00 $40 home dev-1 $2.00 $20 dev-3 $0.50 $5.00");
    expect(shown).toContain("By machine 24 hours 7 days dev-1 $6.00 $60 dev-2 $5.00 $50 dev-3 $0.50 $5.00");
  });
});

describe("ProvidersCard", () => {
  // A provider's figures: day dollars and n tokens today, seven times both
  // this week.
  const onProvider = (provider: string, day: number, n: number) => ({
    provider,
    day_usd: day,
    week_usd: 7 * day,
    day_tokens: {input: n, output: 0, cache_write: 0, cache_read: 0, total: n},
    week_tokens: {input: 7 * n, output: 0, cache_write: 0, cache_read: 0, total: 7 * n},
  });
  const reporting = (id: string, scope: string, providers: ReturnType<typeof onProvider>[], updated_at = "2026-11-03T11:00:00Z"): HubInstance =>
    running(id, "4.5.0", "dev-1", {
      summary: summary({
        instance: {id, server_id: "s", agent_type: "claude", transport: "native", agentapi_version: "4.5.0"},
        spend: {day_usd: 0, week_usd: 0, scope, updated_at, providers},
      }),
    });
  const card = (instances: HubInstance[], names?: ReadonlyMap<string, string>) =>
    text(renderToStaticMarkup(<ProvidersCard instances={instances} names={names} />));

  test("each provider's totals, each scope once, an unpriced one by its tokens", () => {
    const shown = card(
      [
        reporting("a", "s1", [onProvider("default", 2, 200_000), onProvider("gateway", 0, 50_000)]),
        // The same sessions as a, figured a minute earlier: not counted.
        reporting("b", "s1", [onProvider("default", 99, 9_000_000)], "2026-11-03T10:59:00Z"),
        reporting("c", "s2", [onProvider("vllm", 3, 1000), onProvider("gateway", 0, 10_000)]),
        running("d", "4.4.0", "dev-2"),
      ],
      new Map([["gateway", "Team gateway"]]),
    );
    expect(shown).toContain("Each provider's calls, from the agents that report them");
    // The figures: the tokens of the providers other than the agents' own.
    expect(shown).toContain("Other providers, 24 hours 61k Other providers, 7 days 427k");
    expect(shown).toContain("3 of 4 agents report it");
    expect(shown).toContain(
      "By provider 24 hours 7 days vllm $3.00 1k tokens $21 7k tokens Own provider $2.00 200k tokens $14 1.4M tokens Team gateway no price 60k tokens no price 420k tokens",
    );
  });

  test("nothing while every call went to the agents' own provider", () => {
    expect(card([reporting("a", "s1", [onProvider("default", 2, 200_000)])])).toBe("");
    expect(card([running("d", "4.4.0", "dev-2")])).toBe("");
  });
});
