import {afterEach, describe, expect, test} from "bun:test";
import {withDOM} from "@/test/dom";
import {button, mount, type Mounted} from "@/test/render";
import type {DispatchHost, DispatchJob} from "@/lib/hub-dispatch";
import {MachinesStrip} from "./machines-strip";

withDOM();

const host = (hostname: string, online: boolean): DispatchHost => ({
  hostname,
  url: `http://${hostname}:3291`,
  agentapi_version: "3.15.2",
  agents: ["claude"],
  default_agent: "claude",
  repos: ["acme/widget"],
  jobs: [],
  max_jobs: 2,
  active: 0,
  clone: false,
  online,
  credentials_available: false,
  hub_credentials: true,
  heartbeat_seconds: 60,
  last_seen: "2026-11-09T10:00:00Z",
});

const working = {key: "acme/widget#12", repo: "acme/widget", issue: 12, host: "busy", agent: "claude", state: "working"} as DispatchJob;

let view: Mounted | undefined;
afterEach(async () => {
  await view?.unmount();
  view = undefined;
});

// The dialog the button opens is Radix's, which the browser checks (see
// @/test/dom).
describe("forgetting a machine from the strip", () => {
  test("only one offline whose jobs ended has the button on", async () => {
    view = await mount(<MachinesStrip hosts={[host("up", true), host("old", false), host("busy", false)]} jobs={[working]} onForget={async () => {}} />);
    expect(button(view.container, /^Forget up/)).toBeUndefined();
    expect(button(view.container, "Can't forget busy: Stop its job first")?.disabled).toBe(true);
    expect(button(view.container, "Forget old…")?.disabled).toBe(false);
  });
});
