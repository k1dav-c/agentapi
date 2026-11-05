import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {HubInstance} from "@/lib/hub";
import {InstanceBar} from "./instance-bar";
import {machineAgents, MachineSwitch, shortAgent} from "./machine-tabs";

const on = (id: string, hostname: string | undefined, agentType: string, overrides: Partial<HubInstance> = {}): HubInstance => ({
  id,
  name: "home",
  url: `http://${id}`,
  hostname,
  agent_type: agentType,
  source: "webhook",
  last_seen: "2026-11-01T09:59:00Z",
  connection: "connected",
  state: "idle",
  ...overrides,
});

const all = [
  on("dev-1:2", "dev-1", "codex", {state: "needs-you", summary: {attention: "request"} as HubInstance["summary"]}),
  on("dev-1:1", "dev-1", "claude", {state: "working"}),
  on("dev-1:3", "dev-1", "pi"),
  on("dev-2:1", "dev-2", "claude"),
  on("x", undefined, "codex"),
];

describe("machineAgents", () => {
  test("the agents of the open one's machine, by agent, with their states", () => {
    expect(machineAgents(all, all[0]).map(({id, agent, state}) => [id, agent, state])).toEqual([
      ["dev-1:1", "Claude Code", "working"],
      ["dev-1:2", "Codex", "needs-you"],
      ["dev-1:3", "Pi", "idle"],
    ]);
  });

  test("none on a machine of one, or one that doesn't say", () => {
    expect(machineAgents(all, all[3])).toEqual([]);
    expect(machineAgents(all, all[4])).toEqual([]);
  });

  test("a tab names Claude Code short", () => {
    expect(shortAgent("claude", "Claude Code")).toBe("Claude");
    expect(shortAgent("codex", "Codex")).toBe("Codex");
  });
});

describe("MachineSwitch", () => {
  test("a button per agent, the open one marked, each with its state's dot", () => {
    const html = renderToStaticMarkup(<MachineSwitch agents={machineAgents(all, all[0])} current="dev-1:2" onOpen={() => {}} />);
    expect(html).toContain('role="group" aria-label="Agents on this machine"');
    expect(html.match(/<button/g)).toHaveLength(3);
    expect(html).toContain('aria-current="true" aria-label="Codex, home: Needs your answer (open)"');
    expect(html).toContain('aria-label="Open Claude Code, home: Working"');
    expect(html).toContain('data-tab-state="needs-you"');
    expect(html).toContain("bg-state-needs");
    expect(html).toContain("bg-state-working");
  });

  test("nothing for a machine of one", () => {
    expect(renderToStaticMarkup(<MachineSwitch agents={machineAgents(all, all[3])} current="dev-2:1" onOpen={() => {}} />)).toBe("");
  });

  test("in the hub's bar above the chat, and not without other agents", () => {
    const bar = (instance: HubInstance) =>
      renderToStaticMarkup(<InstanceBar instance={instance} machine={machineAgents(all, instance)} hubVersion="3.1.0" root="" onBack={() => {}} onOpen={() => {}} />);
    expect(bar(all[1])).toContain("data-machine-switch");
    expect(bar(all[3])).not.toContain("data-machine-switch");
  });
});
