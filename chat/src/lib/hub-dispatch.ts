// The hub's issue dispatch (lib/dispatch): the machines whose launchers
// report to the hub, the jobs (an issue an agent works on), the hub's
// GitHub App and its repositories' issues, and POST /api/dispatch, which
// starts, messages and stops the work on an issue. The types are the
// hub's (hub-schema.ts); the calls throw HubAPIError as the rest of the
// hub's API does.

import type {HubWire} from "./hub";
import {hubCall} from "./hub-api";
import type {Fetcher} from "./hub-update";

export type DispatchHost = HubWire["DispatchHost"];
export type DispatchJob = HubWire["DispatchJob"];
export type DispatchRun = HubWire["DispatchRun"];
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

export function createDispatchAPI(root: string, fetcher: Fetcher = (url, init) => fetch(url, init)) {
  const fetchHub = hubCall(root, fetcher);
  const call = async <T>(path: string, init: RequestInit = {}): Promise<T> => (await (await fetchHub(path, init)).json()) as T;
  const post = (body: unknown): RequestInit => ({method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  return {
    github: () => call<GitHubStatus>("/api/github"),
    hosts: async () => (await call<HubWire["DispatchHostsBody"]>("/api/hosts")).hosts ?? [],
    jobs: async () => (await call<HubWire["DispatchJobsBody"]>("/api/jobs")).jobs ?? [],
    repos: async () => (await call<HubWire["DispatchReposBody"]>("/api/github/repos")).repos ?? [],
    issues: async (repo: string) => (await call<HubWire["DispatchIssuesBody"]>(`/api/github/issues?repo=${encodeURIComponent(repo)}`)).issues ?? [],
    dispatch: (request: DispatchRequest) => call<DispatchResponse>("/api/dispatch", post(request)),
    createIssue: (request: CreateIssueRequest) => call<CreateIssueResponse>("/api/github/issues", post(request)),
  };
}
