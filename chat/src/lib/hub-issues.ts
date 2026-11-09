// The Issues view's model: the issues of the repositories in view and the
// jobs on them, as one board of cards by stage; who an issue would go to
// (the suggestion a tap on Assign takes) and the other picks; and the
// timeline of a job's runs. Pure, so it is tested without a page.

import type {HubInstance} from "./hub";
import {instanceState} from "./hub";
import {active, agentLabel, hostAgents, hostsFor, jobKey, type DispatchHost, type DispatchJob, type DispatchMessage, type DispatchRun, type GitHubIssue} from "./hub-dispatch";

// Stage is where an issue is in its pipeline.
export type Stage = "todo" | "working" | "review" | "failed" | "done";

export function stageOf(job: Pick<DispatchJob, "state"> | undefined): Stage {
  switch (job?.state) {
    case undefined:
      return "todo";
    case "starting":
    case "working":
      return "working";
    case "review":
      return "review";
    case "failed":
      return "failed";
    case "done":
    case "stopped":
      return "done";
  }
  return "working";
}

// Card is an issue on the board: from GitHub, from the hub's jobs, or both.
export interface Card {
  key: string;
  repo: string;
  number: number;
  title: string;
  url: string;
  labels: string[];
  body: string;
  job?: DispatchJob;
  stage: Stage;
  // waiting: the job's agent waits on you (an approval, a question, a
  // login, a usage limit), as the hub's stream says of its instance.
  waiting: boolean;
}

// A label the dispatch adds itself (agent:claude), which the card's agent
// says already.
const agentTag = /^agent(:|$)/;

// boardCards are the cards of the repositories in scope ("" is all): each
// open issue, and each job, of an issue open or not.
export function boardCards(issues: ReadonlyMap<string, GitHubIssue[]>, jobs: DispatchJob[], scope: string, instances?: ReadonlyMap<string, HubInstance>): Card[] {
  const inScope = (repo: string) => scope === "" || repo === scope;
  const cards = new Map<string, Card>();
  for (const [repo, list] of issues) {
    if (!inScope(repo)) continue;
    for (const issue of list) {
      const key = jobKey(repo, issue.number);
      cards.set(key, {
        key,
        repo,
        number: issue.number,
        title: issue.title,
        url: issue.html_url,
        labels: (issue.labels ?? []).map((label) => label.name).filter((name) => !agentTag.test(name)),
        body: issue.body ?? "",
        stage: "todo",
        waiting: false,
      });
    }
  }
  for (const job of jobs) {
    if (!inScope(job.repo)) continue;
    const card = cards.get(job.key);
    const instance = instances?.get(job.instance_id);
    const waiting = active(job) && job.state !== "failed" && instance !== undefined && instanceState(instance) === "needs-you";
    cards.set(job.key, {
      key: job.key,
      repo: job.repo,
      number: job.issue,
      title: card?.title || job.title || job.key,
      url: card?.url || job.issue_url,
      labels: card?.labels ?? [],
      body: card?.body ?? "",
      job,
      stage: stageOf(job),
      waiting,
    });
  }
  return [...cards.values()];
}

// SectionID is one of the board's sections, in the order they show: what
// needs you first, as on the hub's overview.
export type SectionID = "attention" | "working" | "todo" | "done";

export const sections: {id: SectionID; title: string}[] = [
  {id: "attention", title: "Needs you"},
  {id: "working", title: "Working"},
  {id: "todo", title: "To do"},
  {id: "done", title: "Done"},
];

export function sectionOf(card: Pick<Card, "stage" | "waiting">): SectionID {
  if (card.stage === "failed" || card.stage === "review" || card.waiting) return "attention";
  if (card.stage === "working") return "working";
  return card.stage;
}

const time = (at: string | undefined) => (at ? Date.parse(at) || 0 : 0);

// Within needs you: failures first, then an agent that waits, then
// reviews; working, the longest at it first; to do, the newest issue
// first; done, the latest first.
const attentionRank = (card: Card) => (card.stage === "failed" ? 0 : card.waiting ? 1 : 2);

export function bySection(cards: Card[]): Record<SectionID, Card[]> {
  const out: Record<SectionID, Card[]> = {attention: [], working: [], todo: [], done: []};
  for (const card of cards) out[sectionOf(card)].push(card);
  out.attention.sort((a, b) => attentionRank(a) - attentionRank(b) || time(b.job?.updated_at) - time(a.job?.updated_at));
  out.working.sort((a, b) => time(a.job?.created_at) - time(b.job?.created_at));
  out.todo.sort((a, b) => a.repo.localeCompare(b.repo) || b.number - a.number);
  out.done.sort((a, b) => time(b.job?.updated_at) - time(a.job?.updated_at));
  return out;
}

// The pipeline's stages, as the rail shows them.
export const pipeline: {stage: Exclude<Stage, "failed">; label: string}[] = [
  {stage: "todo", label: "To do"},
  {stage: "working", label: "In progress"},
  {stage: "review", label: "In review"},
  {stage: "done", label: "Done"},
];

export function stageCounts(cards: Card[]): Record<Stage | "waiting", number> {
  const counts: Record<Stage | "waiting", number> = {todo: 0, working: 0, review: 0, failed: 0, done: 0, waiting: 0};
  for (const card of cards) {
    counts[card.stage]++;
    if (card.waiting) counts.waiting++;
  }
  return counts;
}

// needsYou counts the jobs that need you, of every repository: failed, in
// review, or with an agent that waits on you; the Issues button's badge.
export function needsYou(jobs: DispatchJob[], instances?: ReadonlyMap<string, HubInstance>): number {
  return boardCards(new Map(), jobs, "", instances).filter((card) => sectionOf(card) === "attention").length;
}

// Option is an agent on a machine an issue could go to.
export interface Option {
  agent: string;
  host: DispatchHost;
  // hasRepo: the machine has the repository; otherwise it clones it.
  hasRepo: boolean;
}

// The agent a machine starts for "": its default.
const agentOn = (host: DispatchHost, agent: string) => agent || host.default_agent;

// Unavailable is a machine that can't take an issue, and why.
interface Unavailable {
  host: DispatchHost;
  reason: string;
}

// whyNot says why host can't start an issue of repo with agent, or
// undefined when it can.
export function whyNot(host: DispatchHost, repo: string, agent: string): string | undefined {
  if (!host.online) return "offline";
  if (!(host.agents ?? []).includes(agent)) return `doesn't run ${agentLabel(agent)}`;
  if (host.active >= host.max_jobs) return `full, ${host.active} of ${host.max_jobs} jobs`;
  if (!(host.repos ?? []).includes(repo) && !host.clone) return "no repo, doesn't clone";
  return undefined;
}

// assignOptions are the picks for an issue of repo, by agent (those of the
// most machines first): each agent's machines as the hub would rank them,
// and those that can't take it with why.
export function assignOptions(hosts: DispatchHost[], repo: string): {agent: string; options: Option[]; unavailable: Unavailable[]}[] {
  return hostAgents(hosts).map((agent) => ({
    agent,
    options: rank(hostsFor(hosts, repo, agent), repo).map((host) => ({agent, host, hasRepo: (host.repos ?? []).includes(repo)})),
    unavailable: hosts
      .map((host) => ({host, reason: whyNot(host, repo, agent)}))
      .filter((entry): entry is Unavailable => entry.reason !== undefined)
      .sort((a, b) => a.host.hostname.localeCompare(b.host.hostname)),
  }));
}

// rank orders machines as the hub picks one: those with the repository,
// then the least busy.
function rank(hosts: DispatchHost[], repo: string): DispatchHost[] {
  const has = (host: DispatchHost) => Number((host.repos ?? []).includes(repo));
  return [...hosts].sort((a, b) => has(b) - has(a) || a.active / a.max_jobs - b.active / b.max_jobs || a.hostname.localeCompare(b.hostname));
}

// suggest is who a tap on Assign gives an issue of repo to: preferred, an
// agent picked before, where one can take it; otherwise the machine the
// hub would pick, with its default agent.
export function suggest(hosts: DispatchHost[], repo: string, preferred = ""): Option | undefined {
  if (preferred) {
    const [host] = rank(hostsFor(hosts, repo, preferred), repo);
    if (host) return {agent: preferred, host, hasRepo: (host.repos ?? []).includes(repo)};
  }
  const [host] = rank(hostsFor(hosts, repo, ""), repo);
  return host ? {agent: agentOn(host, ""), host, hasRepo: (host.repos ?? []).includes(repo)} : undefined;
}

// switchAgents are the agents a job could switch to: the others its
// machine runs, since a switch keeps the worktree there.
export function switchAgents(hosts: DispatchHost[], job: Pick<DispatchJob, "host" | "agent">): string[] {
  const host = hosts.find((h) => h.hostname === job.host);
  if (!host?.online) return [];
  return (host.agents ?? []).filter((agent) => agent !== job.agent);
}

// whyNoOption says why no machine can take an issue of repo.
export function whyNoOption(hosts: DispatchHost[], repo: string): string {
  const online = hosts.filter((host) => host.online);
  if (hosts.length === 0) return "No machine's launcher reports to the hub yet";
  if (online.length === 0) return "Every machine is offline";
  if (online.every((host) => host.active >= host.max_jobs)) return "Every machine is at capacity";
  return `No machine online has ${repo} or may clone it`;
}

// runSeconds is how long a run went on: to its end, or to now.
export function runSeconds(run: Pick<DispatchRun, "started_at" | "ended_at">, now: number): number {
  const start = time(run.started_at);
  if (!start) return 0;
  return Math.max(0, ((run.ended_at ? time(run.ended_at) : now) - start) / 1000);
}

// formatRunDuration is how long a run (or a job's agents) worked, in its
// two largest units: "45s", "12m", "1h 05m", "2d 3h". The chat's clock
// (lib/clock.ts) has formatDuration, a stopwatch's "1:05".
export function formatRunDuration(seconds: number): string {
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}

// jobTotals add up a job's runs.
export function jobTotals(job: Pick<DispatchJob, "runs">, now: number): {usd: number; seconds: number; agentSeconds: number; tokens: number; runs: number} {
  const runs = job.runs ?? [];
  let usd = 0;
  let seconds = 0;
  let agentSeconds = 0;
  let tokens = 0;
  for (const run of runs) {
    usd += run.cost?.usd ?? 0;
    seconds += runSeconds(run, now);
    agentSeconds += run.cost?.work_seconds ?? 0;
    const t = run.cost?.tokens;
    if (t) tokens += t.input + t.output + t.cache_read + t.cache_write;
  }
  return {usd, seconds, agentSeconds, tokens, runs: runs.length};
}

// endReason says how a run ended, for its line in the timeline.
export function endReason(run: Pick<DispatchRun, "end_reason" | "ended_at" | "missed">, next?: Pick<DispatchRun, "agent">): string {
  if (!run.ended_at) return "";
  switch (run.end_reason) {
    case "replaced":
      return next ? `Handed over to ${agentLabel(next.agent)}` : "Handed over";
    case "failed":
      return "Failed";
    case "done":
      return "Finished";
    case "review":
      return "Opened the pull request";
    case "":
    case undefined:
      return run.missed ? "Gone from its machine" : "Ended";
    default:
      return run.end_reason[0].toUpperCase() + run.end_reason.slice(1);
  }
}

// failureText is a failed job's error as the page says it: its first
// letter up, without the log's path (logPath), which the detail has apart.
export function failureText(error: string | undefined): string {
  const text = (error ?? "").replace(/;?\s*see \S+$/, "").trim();
  if (!text) return "The job failed";
  return text[0].toUpperCase() + text.slice(1);
}

// logPath is the log file a failure's message points at.
export function logPath(error: string | undefined): string | undefined {
  return error?.match(/(\/[^\s;,'"]+\.log)\b/)?.[1];
}

// TimelineEntry is a line of a job's timeline: a run, or a message its
// agent was sent.
export type TimelineEntry =
  | {kind: "run"; at: string; run: DispatchRun; index: number; next?: DispatchRun; current: boolean}
  | {kind: "message"; at: string; message: DispatchMessage};

// timeline is a job's runs and the messages sent it, the latest first.
export function timeline(job: Pick<DispatchJob, "runs" | "state" | "messages">): TimelineEntry[] {
  const runs = job.runs ?? [];
  const entries: TimelineEntry[] = runs.map((run, index) => ({
    kind: "run",
    at: run.started_at,
    run,
    index,
    next: runs[index + 1],
    current: index === runs.length - 1 && !run.ended_at && active(job),
  }));
  for (const message of job.messages ?? []) entries.push({kind: "message", at: message.at, message});
  return entries.sort((a, b) => time(b.at) - time(a.at) || (a.kind === "run" && b.kind === "run" ? b.index - a.index : 0));
}

// messageFrom says who sent a message: you on the hub, or a review or
// comment's author on GitHub.
export function messageFrom(message: Pick<DispatchMessage, "source" | "author" | "review_state" | "comments">): string {
  switch (message.source) {
    case "hub":
      return "You";
    case "review": {
      const state = message.review_state === "changes_requested" ? "requested changes" : "reviewed";
      const lines = message.comments ? `, ${message.comments} ${message.comments === 1 ? "comment" : "comments"}` : "";
      return `${message.author || "A reviewer"} ${state}${lines}`;
    }
    default:
      return `${message.author || "Someone"} commented`;
  }
}

// headline is what the view's title says of the board.
export function headline(counts: Record<SectionID, number>): string {
  if (counts.attention > 0) return `${counts.attention} ${counts.attention === 1 ? "issue needs" : "issues need"} you`;
  if (counts.working > 0) return `${counts.working} ${counts.working === 1 ? "agent" : "agents"} at work`;
  if (counts.todo > 0) return `${counts.todo} ${counts.todo === 1 ? "issue" : "issues"} to hand out`;
  return "Nothing on the board";
}

// repoName is a repository's short name, for a card in a board of many.
export function repoName(repo: string): string {
  return repo.slice(repo.indexOf("/") + 1);
}
