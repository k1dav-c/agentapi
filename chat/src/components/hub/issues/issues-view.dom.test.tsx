import {afterEach, describe, expect, test} from "bun:test";
import {act} from "react";
import type {DispatchBoard} from "@/lib/hub";
import type {DispatchHost, DispatchJob} from "@/lib/hub-dispatch";
import {withDOM} from "@/test/dom";
import {json, mockFetch, restoreFetch} from "@/test/fetch";
import {button, mount, settle, type Mounted} from "@/test/render";
import {IssuesView} from "./issues-view";

withDOM();

const host: DispatchHost = {
  hostname: "pve-dev",
  url: "http://pve:3291",
  agentapi_version: "3.14.0",
  agents: ["claude", "codex"],
  default_agent: "claude",
  repos: ["acme/widget"],
  jobs: [],
  max_jobs: 4,
  active: 2,
  clone: true,
  online: true,
  credentials_available: false,
  hub_credentials: true,
  heartbeat_seconds: 60,
  last_seen: "2026-11-09T10:59:00Z",
};

const job = (issue: number, over: Partial<DispatchJob> = {}): DispatchJob => ({
  key: `acme/widget#${issue}`,
  repo: "acme/widget",
  issue,
  title: `Issue ${issue}`,
  host: "pve-dev",
  agent: "claude",
  instance_id: `pve-dev~acme~widget~${issue}`,
  state: "working",
  issue_url: `https://github.com/acme/widget/issues/${issue}`,
  credentials: true,
  created_at: "2026-11-09T10:00:00Z",
  updated_at: "2026-11-09T10:00:00Z",
  round: 1,
  runs: [{round: 1, agent: "claude", host: "pve-dev", instance_id: `pve-dev~acme~widget~${issue}`, started_at: "2026-11-09T10:00:00Z", cost: {usd: 0, calls: 0, tasks: 0, work_seconds: 0, tokens: {input: 0, output: 0, cache_read: 0, cache_write: 0}}}],
  ...over,
});

const board: DispatchBoard = {hosts: [host], jobs: [job(12), job(13)]};

let view: Mounted | undefined;
afterEach(async () => {
  await view?.unmount();
  view = undefined;
  restoreFetch();
  localStorage.clear();
});

const notConnected = () => mockFetch(json({connected: false, installations: []}), json({connected: false, installations: []}));

const issues = (selectedKey: string | null, live: DispatchBoard = board) => (
  <IssuesView root="/hub" instances={new Map()} live={live} selectedKey={selectedKey} onSelect={() => {}} onClose={() => {}} onOpenChat={() => {}} />
);

describe("IssuesView's detail", () => {
  test("a Stop being confirmed doesn't carry over to the next issue opened", async () => {
    notConnected();
    view = await mount(issues("acme/widget#12"));
    await settle();
    await act(async () => button(document.body, /Stop$/)!.click());
    expect(document.querySelector("[role=alertdialog]")?.textContent).toContain("agent/issue-12");

    await view.rerender(issues("acme/widget#13"));
    await settle();
    expect(document.querySelector("[role=alertdialog]")?.textContent ?? "none").toBe("none");
  });

  test("a message half written to one agent isn't left for the next issue's", async () => {
    notConnected();
    view = await mount(issues("acme/widget#12"));
    await settle();
    const draft = document.querySelector<HTMLTextAreaElement>("textarea[aria-label^='Message to']")!;
    await act(async () => {
      // React reads the value through the native setter.
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(draft, "for #12 only");
      draft.dispatchEvent(new Event("input", {bubbles: true}));
    });
    expect(button(document.body, "Send")!.disabled).toBe(false);

    await view.rerender(issues("acme/widget#13"));
    await settle();
    expect(document.querySelector<HTMLTextAreaElement>("textarea[aria-label^='Message to']")!.value).toBe("");
  });
});
