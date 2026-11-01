import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance} from "@/lib/hub";
import {VersionsCard} from "./overview";

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

// The text of html, without its tags.
const text = (html: string) => html.replace(/<[^>]+>/g, " ").replaceAll("&#x27;", "'").replace(/\s+/g, " ");

describe("VersionsCard", () => {
  const instances = [
    running("a", "1.6.1", "dev-2"),
    running("b", "1.6.1", "dev-1"),
    running("c", "2.1.0", "dev-3"),
    running("d", "2.0.0", "dev-3"),
    running("e", "1.2.0", "dev-4", {connection: "stopped", state: "stopped"}),
  ];

  test("counts each version and those older than the hub, and says how to update them", () => {
    const html = renderToStaticMarkup(<VersionsCard instances={instances} hubVersion="2.1.0" />);
    const shown = text(html);
    expect(shown).toContain("the hub runs v2.1.0");
    expect(shown).toContain("Older than the hub 3 of 4 connected agents");
    expect(shown).toContain("v2.1.0 the hub's dev-3 1 v2.0.0 older dev-3 1 v1.6.1 older dev-1 , dev-2 2");
    expect(shown).not.toContain("v1.2.0");
    expect(shown).toContain("sudo agentapi update --restart");
    expect(shown).toContain("before v2.1.0, update, then restart each AgentAPI once");
  });

  test("with every agent on the hub's version, there is nothing to update", () => {
    const html = renderToStaticMarkup(<VersionsCard instances={[running("c", "2.1.0", "dev-3")]} hubVersion="2.1.0" />);
    expect(text(html)).toContain("Older than the hub 0 of 1 connected agent");
    expect(html).not.toContain("sudo agentapi update");
  });

  test("a development build of the hub tells no version older", () => {
    const html = renderToStaticMarkup(<VersionsCard instances={instances} hubVersion="0.0.0-dev" />);
    expect(text(html)).toContain("The hub is a development build");
    expect(html).not.toContain("Older than the hub");
    expect(html).not.toContain(">older</span>");
    expect(html).not.toContain("sudo agentapi update");
  });

  test("nothing to show without a connected agent", () => {
    expect(renderToStaticMarkup(<VersionsCard instances={[instances[4]]} hubVersion="2.1.0" />)).toBe("");
  });
});
