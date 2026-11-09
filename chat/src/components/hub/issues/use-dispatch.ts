"use client";

import {useCallback, useEffect, useMemo, useState} from "react";
import {toast} from "sonner";
import {HubAPIError, hubErrorText} from "@/lib/hub-api";
import {
  agentLabel,
  createDispatchAPI,
  jobKey,
  pendingAnswers,
  withAnswers,
  type CreateIssueRequest,
  type DispatchHost,
  type DispatchJob,
  type GitHubIssue,
  type GitHubRepo,
  type GitHubStatus,
} from "@/lib/hub-dispatch";
import type {DispatchBoard} from "@/lib/hub";
import {useHubSetting} from "@/lib/hub-settings";

// The most repositories whose issues All reads: the latest pushed or
// changed, as the hub lists them.
export const recentRepos = 20;

// Load is a read that is under way, went through, or failed.
export type Load<T> = {state: "loading"} | {state: "ready"; value: T} | {state: "failed"; error: string};

// IssueTarget is the issue an action is on.
export interface IssueTarget {
  repo: string;
  number: number;
}

export interface DispatchActions {
  // assign starts work: agent "" is the machine's default, host "" lets
  // the hub pick.
  assign: (target: IssueTarget, agent: string, host: string) => Promise<boolean>;
  retry: (job: DispatchJob) => Promise<boolean>;
  switchTo: (job: DispatchJob, agent: string) => Promise<boolean>;
  stop: (job: DispatchJob) => Promise<boolean>;
  send: (job: DispatchJob, message: string) => Promise<boolean>;
  createIssue: (request: CreateIssueRequest) => Promise<GitHubIssue | undefined>;
  refresh: () => void;
}

// What a refused dispatch's status means to a person; the hub's message
// says the rest (lib/dispatch: 503 no machine or it is offline, 422 the
// machine lacks the repository or the agent, 412 GitHub isn't set up, 409
// no room).
const refusals: Record<number, string> = {
  409: "The machine has no room",
  412: "GitHub isn't set up for it",
  422: "That machine can't take it",
  503: "No machine can take it right now",
};

function refusal(error: unknown): {title?: string; description: string} {
  const description = hubErrorText(error);
  return {title: error instanceof HubAPIError ? refusals[error.status] : undefined, description};
}

// useDispatch reads the dispatch while the Issues view is open: the hub's
// GitHub App, its repositories and their open issues, and the machines and
// the jobs from live (the hub's stream; undefined until its first dispatch
// event); and does what the view asks of them.
// githubChanges counts the changes to the app made elsewhere (the hub's
// settings): each reads it again.
export function useDispatch(root: string, live?: DispatchBoard, githubChanges = 0) {
  const api = useMemo(() => createDispatchAPI(root), [root]);
  const [scope, setScope] = useHubSetting("issuesRepo");
  const [preferred, setPreferred] = useHubSetting("issuesAgent");
  const [github, setGitHub] = useState<Load<GitHubStatus>>({state: "loading"});
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [issues, setIssues] = useState<ReadonlyMap<string, GitHubIssue[]>>(new Map());
  // The repositories whose issues are being read, and those that failed.
  const [reading, setReading] = useState(false);
  const [issuesError, setIssuesError] = useState("");
  // The jobs the hub answered an action with, until its stream has them
  // as new.
  const [answered, setAnswered] = useState<ReadonlyMap<string, DispatchJob>>(new Map());
  const streamed = live?.jobs;
  useEffect(() => {
    if (streamed) setAnswered((answers) => pendingAnswers(answers, streamed));
  }, [streamed]);
  // The keys of the issues an action is under way on.
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());

  const readGitHub = useCallback(async () => {
    try {
      const status = await api.github();
      setGitHub({state: "ready", value: status});
      if (status.connected) setRepos(await api.repos());
    } catch (e) {
      setGitHub({state: "failed", error: hubErrorText(e)});
    }
  }, [api]);

  // The repositories whose issues the scope shows.
  const repoNames = useMemo(() => repos.map((repo) => repo.full_name), [repos]);
  const scoped = useMemo(() => (scope && repoNames.includes(scope) ? [scope] : repoNames.slice(0, recentRepos)), [scope, repoNames]);
  const readIssues = useCallback(async () => {
    if (scoped.length === 0) return;
    setReading(true);
    const results = await Promise.allSettled(scoped.map((repo) => api.issues(repo)));
    setIssues((previous) => {
      const next = new Map(previous);
      results.forEach((result, i) => {
        if (result.status === "fulfilled") next.set(scoped[i], result.value);
      });
      return next;
    });
    const failed = results.flatMap((result, i) => (result.status === "rejected" ? [`${scoped[i]}: ${hubErrorText(result.reason)}`] : []));
    setIssuesError(failed.join("\n"));
    setReading(false);
  }, [api, scoped]);

  useEffect(() => {
    void readGitHub();
  }, [readGitHub, githubChanges]);
  useEffect(() => {
    void readIssues();
  }, [readIssues]);

  const hosts: Load<DispatchHost[]> = live ? {state: "ready", value: live.hosts} : {state: "loading"};
  const jobs = useMemo(() => withAnswers(streamed ?? [], answered), [streamed, answered]);

  // A job the hub answered with goes on the board at once, before the
  // stream tells of it.
  const merge = (job: DispatchJob | undefined) => {
    if (job) setAnswered((all) => new Map(all).set(job.key, job));
  };

  // run does an action on key's issue and says how it went; it resolves to
  // what the hub answered, undefined if it didn't go through.
  const run = useCallback(async <T>(key: string, what: string, done: string, action: () => Promise<T>): Promise<T | undefined> => {
    setBusy((keys) => new Set(keys).add(key));
    try {
      const result = await action();
      toast.success(done);
      return result;
    } catch (e) {
      const {title, description} = refusal(e);
      toast.error(title ? `Couldn't ${what}: ${title.toLowerCase()}` : `Couldn't ${what}`, {description});
      return undefined;
    } finally {
      setBusy((keys) => {
        const left = new Set(keys);
        left.delete(key);
        return left;
      });
    }
  }, []);

  const actions: DispatchActions = {
    assign: async ({repo, number}, agent, host) => {
      const key = jobKey(repo, number);
      const response = await run(key, `assign ${key}`, `${key} went to ${agent ? agentLabel(agent) : "the machine's default agent"}${host ? ` on ${host}` : ""}`, () =>
        api.dispatch({action: "start", repo, issue: number, agent, host}),
      );
      merge(response?.job);
      return response !== undefined;
    },
    retry: async (job) => {
      const response = await run(job.key, `retry ${job.key}`, `${agentLabel(job.agent)} starts ${job.key} again`, () =>
        api.dispatch({action: "start", repo: job.repo, issue: job.issue, agent: job.agent}),
      );
      merge(response?.job);
      return response !== undefined;
    },
    switchTo: async (job, agent) => {
      setPreferred(agent);
      const response = await run(job.key, `switch ${job.key} to ${agentLabel(agent)}`, `${agentLabel(agent)} takes over ${job.key}`, () =>
        api.dispatch({action: "start", repo: job.repo, issue: job.issue, agent}),
      );
      merge(response?.job);
      return response !== undefined;
    },
    stop: async (job) => {
      const response = await run(job.key, `stop ${job.key}`, `Stopped ${job.key}`, () => api.dispatch({action: "stop", repo: job.repo, issue: job.issue}));
      merge(response?.job);
      return response !== undefined;
    },
    send: async (job, message) => {
      const response = await run(job.key, `send ${job.key} a message`, `Sent to ${agentLabel(job.agent)}`, () =>
        api.dispatch({action: "send", repo: job.repo, issue: job.issue, message, author: "hub"}),
      );
      merge(response?.job);
      return response !== undefined;
    },
    createIssue: async (request) => {
      const response = await run(`new ${request.repo}`, `open the issue in ${request.repo}`, `Opened the issue in ${request.repo}`, () => api.createIssue(request));
      if (!response) return undefined;
      merge(response.job);
      setIssues((all) => new Map(all).set(request.repo, [response.issue, ...(all.get(request.repo) ?? []).filter((i) => i.number !== response.issue.number)]));
      return response.issue;
    },
    refresh: () => void readIssues(),
  };

  return {
    scope: scope && repoNames.includes(scope) ? scope : "",
    setScope,
    preferred,
    setPreferred,
    github,
    retryGitHub: readGitHub,
    repos,
    issues,
    reading,
    issuesError,
    hosts,
    jobs,
    busy,
    actions,
  };
}

export type Dispatch = ReturnType<typeof useDispatch>;
