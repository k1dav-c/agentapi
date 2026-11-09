// The hub's issue dispatch (lib/dispatch): the machines whose launchers
// report to the hub, the jobs (an issue an agent works on), the hub's
// GitHub App and its repositories' issues, and POST /api/dispatch, which
// starts, messages and stops the work on an issue. The types are the
// hub's (hub-schema.ts); the calls throw HubAPIError as the rest of the
// hub's API does, and for an answer that isn't what its route sends (each
// is checked at the door, as the stream's events are).

import {objectsWith, type HubWire} from "./hub";
import {HubAPIError, hubCall} from "./hub-api";
import type {Fetcher} from "./hub-update";

export type DispatchHost = HubWire["DispatchHost"];
export type DispatchJob = HubWire["DispatchJob"];
export type DispatchRun = HubWire["DispatchRun"];
export type DispatchMessage = HubWire["DispatchMessage"];
export type DispatchRequest = HubWire["DispatchRequest"];
export type DispatchResponse = HubWire["DispatchResponse"];
export type GitHubStatus = HubWire["DispatchGitHubStatus"];
export type GitHubRepo = HubWire["GitHubRepo"];
export type GitHubIssue = HubWire["GitHubIssue"];
export type CreateIssueRequest = HubWire["DispatchCreateIssueRequest"];
export type CreateIssueResponse = HubWire["DispatchCreateIssueResponse"];
export type JobState = DispatchJob["state"];

const agentLabels: Record<string, string> = {claude: "Claude Code", codex: "Codex", pi: "Pi", "native-demo": "Demo"};

// agentLabel names an agent for people; "" is the machine's default.
export function agentLabel(agent: string): string {
  return agent === "" ? "Machine's default" : (agentLabels[agent] ?? agent);
}

// jobKey is a job's key, owner/repo#issue.
export function jobKey(repo: string, issue: number): string {
  return `${repo}#${issue}`;
}

// active reports whether work on the job goes on: a start of it starts
// nothing new unless it asks for another agent.
export function active(job: Pick<DispatchJob, "state">): boolean {
  return job.state !== "done" && job.state !== "stopped";
}

const stateLabels: Record<JobState, string> = {
  starting: "Starting",
  working: "Working",
  review: "In review",
  failed: "Failed",
  done: "Done",
  stopped: "Stopped",
};

export function jobStateLabel(state: JobState): string {
  return stateLabels[state] ?? state;
}

// jobCost adds up a job's runs: its API-equivalent spend.
export function jobCost(job: Pick<DispatchJob, "runs">): number {
  return (job.runs ?? []).reduce((sum, run) => sum + (run.cost?.usd ?? 0), 0);
}

// hostAgents are the agents the machines online run, those of the most
// machines first; each machine's default counts as one it runs.
export function hostAgents(hosts: DispatchHost[]): string[] {
  const counts = new Map<string, number>();
  for (const host of hosts) {
    if (!host.online) continue;
    for (const agent of host.agents ?? []) counts.set(agent, (counts.get(agent) ?? 0) + 1);
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([agent]) => agent);
}

// hostsFor are the machines online that can start an issue of repo with
// agent ("" for their default): it runs the agent and has room, and the
// repository or may clone it. Those with the repository come first.
export function hostsFor(hosts: DispatchHost[], repo: string, agent: string): DispatchHost[] {
  return hosts
    .filter((host) => host.online)
    .filter((host) => (agent === "" ? host.default_agent !== "" : (host.agents ?? []).includes(agent)))
    .filter((host) => host.active < host.max_jobs)
    .filter((host) => (host.repos ?? []).includes(repo) || host.clone)
    .sort((a, b) => Number((b.repos ?? []).includes(repo)) - Number((a.repos ?? []).includes(repo)) || a.hostname.localeCompare(b.hostname));
}

// parseIssueRef reads "owner/repo#12" or an issue's GitHub URL: the issue a
// hub without GitHub dispatches.
export function parseIssueRef(input: string): {repo: string; issue: number} | undefined {
  const text = input.trim();
  const url = text.match(/^https?:\/\/[^/]+\/([\w.-]+\/[\w.-]+)\/(?:issues|pull)\/(\d+)\/?$/);
  const ref = url ?? text.match(/^([\w.-]+\/[\w.-]+)#(\d+)$/);
  // GitHub has no owner or repository named . or ..
  if (!ref || ref[1].split("/").some((part) => part === "." || part === "..")) return undefined;
  const issue = Number(ref[2]);
  return issue > 0 ? {repo: ref[1], issue} : undefined;
}

// prNumber is the number of a pull request's URL.
export function prNumber(url: string | undefined): number | undefined {
  const match = url?.match(/\/pull\/(\d+)/);
  return match ? Number(match[1]) : undefined;
}

// caughtUp reports whether the stream's job is as new as an answer to an
// action on it (updated_at), or newer: the answer has done its part.
function caughtUp(streamed: DispatchJob | undefined, answer: DispatchJob): boolean {
  if (!streamed) return false;
  const at = Date.parse(answer.updated_at);
  return Number.isNaN(at) || Date.parse(streamed.updated_at) >= at;
}

// pendingAnswers are the answers the hub gave the page's actions (by job
// key) that the stream's jobs haven't caught up with: the same map when
// none has.
export function pendingAnswers(answers: ReadonlyMap<string, DispatchJob>, streamed: DispatchJob[]): ReadonlyMap<string, DispatchJob> {
  const byKey = new Map(streamed.map((job) => [job.key, job]));
  const left = [...answers].filter(([key, answer]) => !caughtUp(byKey.get(key), answer));
  return left.length === answers.size ? answers : new Map(left);
}

// withAnswers are the stream's jobs with the answers it hasn't caught up
// with in their place, and those it hasn't sent yet first: an action shows
// on the board as the hub answers it, and the stream takes over from there.
export function withAnswers(streamed: DispatchJob[], answers: ReadonlyMap<string, DispatchJob>): DispatchJob[] {
  const pending = pendingAnswers(answers, streamed);
  if (pending.size === 0) return streamed;
  const keys = new Set(streamed.map((job) => job.key));
  return [...[...pending.values()].filter((job) => !keys.has(job.key)), ...streamed.map((job) => pending.get(job.key) ?? job)];
}

// Fields of an answer, unchecked until read.
type Fields<T> = Partial<Record<keyof T, unknown>>;

const isObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
const isJob = (value: unknown): value is DispatchJob => isObject(value) && typeof value.key === "string" && value.key !== "" && typeof value.state === "string";
const isIssue = (value: unknown): value is GitHubIssue => isObject(value) && typeof value.number === "number";

// The hub's answers, read: what each route sends, or undefined.
const read = {
  github: (body: unknown): GitHubStatus | undefined => {
    if (!isObject(body) || typeof body.connected !== "boolean") return undefined;
    return {...(body as GitHubStatus), installations: objectsWith((body as Fields<GitHubStatus>).installations, "account")};
  },
  repos: (body: unknown): GitHubRepo[] | undefined => (isObject(body) ? objectsWith((body as Fields<HubWire["DispatchReposBody"]>).repos, "full_name") : undefined),
  issues: (body: unknown): GitHubIssue[] | undefined => {
    if (!isObject(body)) return undefined;
    const {issues} = body as Fields<HubWire["DispatchIssuesBody"]>;
    return Array.isArray(issues) ? issues.filter(isIssue) : [];
  },
  dispatch: (body: unknown): DispatchResponse | undefined => (isObject(body) && isJob(body.job) ? (body as DispatchResponse) : undefined),
  createIssue: (body: unknown): CreateIssueResponse | undefined => {
    if (!isObject(body) || !isIssue(body.issue)) return undefined;
    return {issue: body.issue, job: isJob(body.job) ? body.job : undefined};
  },
};

export function createDispatchAPI(root: string, fetcher: Fetcher = (url, init) => fetch(url, init)) {
  const fetchHub = hubCall(root, fetcher);
  // call reads the answer to path with parse; one it can't read throws.
  const call = async <T>(path: string, parse: (body: unknown) => T | undefined, init: RequestInit = {}): Promise<T> => {
    const response = await fetchHub(path, init);
    const value = parse(await response.json().catch(() => undefined));
    if (value === undefined) throw new HubAPIError(`The hub's answer to ${path.split("?")[0]} isn't one the page can read.`, response.status);
    return value;
  };
  const post = (body: unknown): RequestInit => ({method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  return {
    github: () => call("/api/github", read.github),
    repos: () => call("/api/github/repos", read.repos),
    issues: (repo: string) => call(`/api/github/issues?repo=${encodeURIComponent(repo)}`, read.issues),
    dispatch: (request: DispatchRequest) => call("/api/dispatch", read.dispatch, post(request)),
    createIssue: (request: CreateIssueRequest) => call("/api/github/issues", read.createIssue, post(request)),
    // The hub forgets its GitHub App.
    disconnect: async () => void (await fetchHub("/api/github", {method: "DELETE"})),
  };
}
