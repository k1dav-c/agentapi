import {describe, expect, test} from "bun:test";
import {HubAPIError} from "./hub-api";
import {active, createDispatchAPI, hostAgents, hostsFor, jobCost, parseIssueRef, prNumber, type DispatchHost, type DispatchJob} from "./hub-dispatch";

const host = (over: Partial<DispatchHost>): DispatchHost => ({
  hostname: "pve",
  url: "http://pve:3291",
  agentapi_version: "3.9.0",
  agents: ["claude"],
  default_agent: "claude",
  repos: ["acme/widget"],
  jobs: [],
  max_jobs: 2,
  active: 0,
  clone: false,
  online: true,
  credentials_available: false,
  hub_credentials: true,
  heartbeat_seconds: 60,
  last_seen: "2026-11-08T10:00:00Z",
  ...over,
});

const job = (over: Partial<DispatchJob>): DispatchJob => ({
  key: "acme/widget#12",
  repo: "acme/widget",
  issue: 12,
  host: "pve",
  agent: "claude",
  instance_id: "pve~acme~widget~12",
  state: "working",
  issue_url: "https://github.com/acme/widget/issues/12",
  credentials: true,
  created_at: "2026-11-08T10:00:00Z",
  updated_at: "2026-11-08T10:00:00Z",
  runs: [],
  round: 1,
  ...over,
});

describe("the machines", () => {
  const hosts = [
    host({hostname: "a", agents: ["claude", "codex"]}),
    host({hostname: "b", agents: ["codex"], default_agent: "codex", repos: [], clone: true}),
    host({hostname: "c", agents: ["pi"], online: false}),
    host({hostname: "d", agents: ["codex"], repos: [], clone: false}),
  ];

  test("the agents online, the most machines' first", () => {
    expect(hostAgents(hosts)).toEqual(["codex", "claude"]);
  });

  test("the machines that can start an issue: with the repository first, cloning, with room", () => {
    expect(hostsFor(hosts, "acme/widget", "codex").map((h) => h.hostname)).toEqual(["a", "b"]);
    expect(hostsFor(hosts, "acme/widget", "").map((h) => h.hostname)).toEqual(["a", "b"]);
    expect(hostsFor(hosts, "acme/widget", "pi")).toEqual([]);
    expect(hostsFor([host({active: 2})], "acme/widget", "claude")).toEqual([]);
  });
});

describe("jobs", () => {
  test("active until it is done or stopped", () => {
    expect(active(job({state: "review"}))).toBe(true);
    expect(active(job({state: "failed"}))).toBe(true);
    expect(active(job({state: "done"}))).toBe(false);
    expect(active(job({state: "stopped"}))).toBe(false);
  });

  test("the cost of all its runs", () => {
    const run = (usd: number) => ({round: 1, agent: "claude", host: "pve", instance_id: "x", started_at: "", cost: {usd, calls: 0, tasks: 0, work_seconds: 0, tokens: {input: 0, output: 0, cache_read: 0, cache_write: 0}}});
    expect(jobCost(job({runs: [run(1.25), run(0.5)]}))).toBe(1.75);
    expect(jobCost(job({runs: null}))).toBe(0);
  });

  test("an issue by its reference or URL", () => {
    expect(parseIssueRef(" acme/widget#12 ")).toEqual({repo: "acme/widget", issue: 12});
    expect(parseIssueRef("https://github.com/acme/my.repo/issues/7")).toEqual({repo: "acme/my.repo", issue: 7});
    expect(parseIssueRef("acme/widget")).toBeUndefined();
    expect(parseIssueRef("acme/widget#0")).toBeUndefined();
    expect(parseIssueRef("../x#1")).toBeUndefined();
    expect(prNumber("https://github.com/acme/widget/pull/34")).toBe(34);
    expect(prNumber(undefined)).toBeUndefined();
  });
});

describe("createDispatchAPI", () => {
  test("calls the hub's routes, and throws the hub's message", async () => {
    const calls: string[] = [];
    const api = createDispatchAPI("http://hub", async (url, init) => {
      calls.push(`${init.method ?? "GET"} ${url} ${init.body ?? ""}`);
      if (url.endsWith("/api/dispatch")) return new Response(JSON.stringify({code: "no_host", message: "no machine online runs codex"}), {status: 503});
      if (url.includes("/api/github/issues")) return new Response(JSON.stringify({issues: null}));
      return new Response(JSON.stringify({hosts: [host({})]}));
    });
    expect((await api.hosts()).map((h) => h.hostname)).toEqual(["pve"]);
    expect(await api.issues("acme/my repo")).toEqual([]);
    const refused = api.dispatch({action: "start", repo: "acme/widget", issue: 12, agent: "codex"});
    await expect(refused).rejects.toThrow("no machine online runs codex");
    await expect(refused).rejects.toBeInstanceOf(HubAPIError);
    expect(calls).toEqual([
      "GET http://hub/api/hosts ",
      "GET http://hub/api/github/issues?repo=acme%2Fmy%20repo ",
      'POST http://hub/api/dispatch {"action":"start","repo":"acme/widget","issue":12,"agent":"codex"}',
    ]);
  });

  test("the hub's sign-in error, which has error and no message", async () => {
    const api = createDispatchAPI("", async () => new Response(JSON.stringify({error: "sign in to the hub"}), {status: 401}));
    await expect(api.jobs()).rejects.toThrow("sign in to the hub");
  });

  test("a hub that doesn't answer", async () => {
    const api = createDispatchAPI("", async () => {
      throw new TypeError("offline");
    });
    await expect(api.jobs()).rejects.toThrow("The hub didn't answer.");
  });
});
