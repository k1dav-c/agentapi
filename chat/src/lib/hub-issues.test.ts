import {describe, expect, test} from "bun:test";
import type {HubInstance} from "./hub";
import type {DispatchHost, DispatchJob, GitHubIssue} from "./hub-dispatch";
import {
  assignOptions,
  boardCards,
  bySection,
  endReason,
  formatDuration,
  headline,
  jobTotals,
  logPath,
  messageFrom,
  needsYou,
  runSeconds,
  sectionOf,
  stageCounts,
  stageOf,
  suggest,
  switchAgents,
  timeline,
  whyNoOption,
  whyNot,
} from "./hub-issues";

const host = (o: Partial<DispatchHost>): DispatchHost => ({
  hostname: "pve",
  url: "",
  agentapi_version: "3.11.0",
  agents: ["claude", "codex"],
  default_agent: "claude",
  repos: [],
  jobs: [],
  max_jobs: 4,
  active: 0,
  clone: false,
  online: true,
  credentials_available: true,
  hub_credentials: true,
  heartbeat_seconds: 60,
  last_seen: "",
  ...o,
});

const cost = (usd: number, work = 60) => ({usd, calls: 1, tasks: 1, work_seconds: work, tokens: {input: 10, output: 20, cache_read: 30, cache_write: 40}});

const job = (o: Partial<DispatchJob>): DispatchJob => ({
  key: `${o.repo ?? "acme/web"}#${o.issue ?? 1}`,
  repo: "acme/web",
  issue: 1,
  host: "pve",
  agent: "claude",
  instance_id: "pve~1",
  state: "working",
  issue_url: "",
  credentials: true,
  created_at: "2026-11-09T10:00:00Z",
  updated_at: "2026-11-09T10:00:00Z",
  round: 1,
  runs: [],
  ...o,
});

const issue = (number: number, labels: string[] = []): GitHubIssue => ({number, title: `Issue ${number}`, html_url: "", labels: labels.map((name) => ({name})), state: "open", body: "", user: {login: "me"}});

describe("the board", () => {
  test("puts each job's state at a stage", () => {
    expect(stageOf(undefined)).toBe("todo");
    expect([stageOf({state: "starting"}), stageOf({state: "working"}), stageOf({state: "review"}), stageOf({state: "failed"})]).toEqual(["working", "working", "review", "failed"]);
    expect([stageOf({state: "done"}), stageOf({state: "stopped"})]).toEqual(["done", "done"]);
  });

  test("joins the open issues and the jobs, in scope, without the agent labels", () => {
    const issues = new Map([
      ["acme/web", [issue(1, ["bug", "agent:claude"]), issue(2)]],
      ["acme/api", [issue(3)]],
    ]);
    const jobs = [job({issue: 1}), job({repo: "acme/web", issue: 9, state: "done", title: "Closed one"})];
    const all = boardCards(issues, jobs, "");
    expect(all.map((card) => card.key).sort()).toEqual(["acme/api#3", "acme/web#1", "acme/web#2", "acme/web#9"]);
    const one = all.find((card) => card.key === "acme/web#1")!;
    expect(one.labels).toEqual(["bug"]);
    expect(one.stage).toBe("working");
    expect(all.find((card) => card.key === "acme/web#9")!.title).toBe("Closed one");
    expect(boardCards(issues, jobs, "acme/api").map((card) => card.key)).toEqual(["acme/api#3"]);
  });

  test("puts a job whose agent waits on you with what needs you", () => {
    const waiting = {id: "pve~1", state: "needs-you"} as HubInstance;
    const [card] = boardCards(new Map(), [job({})], "", new Map([["pve~1", waiting]]));
    expect(card.waiting).toBe(true);
    expect(sectionOf(card)).toBe("attention");
    expect(stageCounts([card])).toMatchObject({working: 1, waiting: 1});
    // The badge counts them, the failed and the reviews, of every repository.
    const jobs = [job({}), job({issue: 2, state: "failed"}), job({repo: "acme/api", issue: 3, state: "review"}), job({issue: 4, instance_id: "pve~4"}), job({issue: 5, state: "done"})];
    expect(needsYou(jobs, new Map([["pve~1", waiting]]))).toBe(3);
    // A failed job's agent is gone: what it asked doesn't count.
    expect(boardCards(new Map(), [job({state: "failed"})], "", new Map([["pve~1", waiting]]))[0].waiting).toBe(false);
  });

  test("orders the sections: failures first, the longest at work first, the newest issue first", () => {
    const cards = boardCards(
      new Map([["acme/web", [issue(4), issue(7)]]]),
      [
        job({issue: 1, state: "review"}),
        job({issue: 2, state: "failed"}),
        job({issue: 3, created_at: "2026-11-09T11:00:00Z"}),
        job({issue: 5, created_at: "2026-11-09T09:00:00Z"}),
      ],
      "",
    );
    const sections = bySection(cards);
    expect(sections.attention.map((card) => card.number)).toEqual([2, 1]);
    expect(sections.working.map((card) => card.number)).toEqual([5, 3]);
    expect(sections.todo.map((card) => card.number)).toEqual([7, 4]);
    expect(stageCounts(cards)).toEqual({todo: 2, working: 2, review: 1, failed: 1, done: 0, waiting: 0});
  });

  test("says what needs you first in its headline", () => {
    expect(headline({attention: 2, working: 1, todo: 0, done: 0})).toBe("2 issues need you");
    expect(headline({attention: 0, working: 1, todo: 3, done: 0})).toBe("1 agent at work");
    expect(headline({attention: 0, working: 0, todo: 1, done: 0})).toBe("1 issue to hand out");
    expect(headline({attention: 0, working: 0, todo: 0, done: 4})).toBe("Nothing on the board");
  });
});

describe("assigning", () => {
  const hosts = [
    host({hostname: "busy", repos: ["acme/web"], active: 3, max_jobs: 4}),
    host({hostname: "idle", repos: ["acme/web"], active: 0, max_jobs: 2, agents: ["codex"], default_agent: "codex"}),
    host({hostname: "cloner", clone: true, agents: ["claude", "pi"]}),
    host({hostname: "down", online: false, repos: ["acme/web"]}),
  ];

  test("suggests the machine the hub would pick, with its default agent", () => {
    const pick = suggest(hosts, "acme/web");
    expect([pick?.host.hostname, pick?.agent, pick?.hasRepo]).toEqual(["idle", "codex", true]);
    // A machine that clones takes a repository none has.
    expect(suggest(hosts, "acme/new")?.host.hostname).toBe("cloner");
    expect(suggest(hosts, "acme/none-clone")?.hasRepo).toBe(false);
  });

  test("prefers the agent picked before, where a machine runs it", () => {
    expect(suggest(hosts, "acme/web", "claude")?.host.hostname).toBe("busy");
    expect(suggest(hosts, "acme/web", "pi")?.host.hostname).toBe("cloner");
    expect(suggest(hosts, "acme/web", "gemini")?.agent).toBe("codex");
  });

  test("offers every agent online, each on the machines that can take the issue", () => {
    const options = assignOptions(hosts, "acme/web");
    expect(options.map((group) => group.agent)).toEqual(["claude", "codex", "pi"]);
    expect(options[0].options.map((option) => option.host.hostname)).toEqual(["busy", "cloner"]);
    expect(options[1].options.map((option) => option.host.hostname)).toEqual(["idle", "busy"]);
  });

  test("says why a machine can't take it", () => {
    expect(assignOptions(hosts, "acme/web")[2].unavailable.map((u) => `${u.host.hostname}: ${u.reason}`)).toEqual([
      "busy: doesn't run Pi",
      "down: offline",
      "idle: doesn't run Pi",
    ]);
    expect(whyNot(host({active: 4}), "acme/web", "claude")).toBe("full, 4 of 4 jobs");
    expect(whyNot(host({}), "acme/web", "claude")).toBe("no repo, doesn't clone");
    expect(whyNot(host({repos: ["acme/web"]}), "acme/web", "claude")).toBeUndefined();
  });

  test("says why nobody can take it", () => {
    expect(whyNoOption([], "acme/web")).toBe("No machine's launcher reports to the hub yet");
    expect(whyNoOption([host({online: false})], "acme/web")).toBe("Every machine is offline");
    expect(whyNoOption([host({active: 4})], "acme/web")).toBe("Every machine is at capacity");
    expect(whyNoOption([host({})], "acme/web")).toBe("No machine online has acme/web or may clone it");
  });

  test("switches to the other agents of the job's machine, while it is online", () => {
    expect(switchAgents(hosts, {host: "busy", agent: "claude"})).toEqual(["codex"]);
    expect(switchAgents(hosts, {host: "down", agent: "claude"})).toEqual([]);
  });
});

describe("a job's story", () => {
  const now = Date.parse("2026-11-09T11:00:00Z");
  const runs = [
    {round: 1, agent: "claude", host: "pve", instance_id: "a", started_at: "2026-11-09T10:00:00Z", ended_at: "2026-11-09T10:30:00Z", end_reason: "replaced", cost: cost(1.5, 600)},
    {round: 1, agent: "codex", host: "pve", instance_id: "b", started_at: "2026-11-09T10:30:00Z", cost: cost(0.25, 60)},
  ];

  test("adds up its runs", () => {
    expect(jobTotals({runs}, now)).toEqual({usd: 1.75, seconds: 3600, agentSeconds: 660, tokens: 200, runs: 2});
    expect(runSeconds(runs[1], now)).toBe(1800);
  });

  test("tells its runs and the messages sent, the latest first", () => {
    const messages = [{at: "2026-11-09T10:45:00Z", source: "hub" as const, agent: "codex", text: "Also the docs"}];
    const entries = timeline({runs, state: "working", messages});
    expect(entries.map((entry) => entry.kind)).toEqual(["message", "run", "run"]);
    const current = entries[1];
    expect(current.kind === "run" && current.current).toBe(true);
    expect(entries[2].kind === "run" && endReason(entries[2].run, entries[2].next)).toBe("Handed over to Codex");
  });

  test("says who sent a message", () => {
    expect(messageFrom({source: "hub", author: ""})).toBe("You");
    expect(messageFrom({source: "review", author: "dave", review_state: "changes_requested", comments: 2})).toBe("dave requested changes, 2 comments");
    expect(messageFrom({source: "review", author: "dave", review_state: "commented", comments: 1})).toBe("dave reviewed, 1 comment");
    expect(messageFrom({source: "comment", author: "erin"})).toBe("erin commented");
  });

  test("formats durations and finds the log", () => {
    expect([formatDuration(42), formatDuration(720), formatDuration(3900), formatDuration(93600)]).toEqual(["42s", "12m", "1h 05m", "1d 2h"]);
    expect(logPath("its AgentAPI exited 5 times in 10m0s; see /home/dev/.agentapi/jobs/acme~web~9/agentapi.log")).toBe("/home/dev/.agentapi/jobs/acme~web~9/agentapi.log");
    expect(logPath("no log")).toBeUndefined();
  });
});
