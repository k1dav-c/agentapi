"use client";

import {useCallback, useEffect, useMemo, useRef, useState} from "react";
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
} from "@/lib/hub-dispatch";
import type {DispatchBoard} from "@/lib/hub";
import {useHubSetting} from "@/lib/hub-settings";
import type {Load} from "@/lib/load";
import {useHubGitHub} from "../use-hub-github";

// The most repositories whose issues All reads: the latest pushed or
// changed, as the hub lists them.
export const recentRepos = 20;

// What an action is on: an issue (its key), or a new issue in a
// repository.
type ActionOn = {issue: string} | {newIn: string};

// A repository whose issues the hub couldn't read, and why.
export interface IssuesFailure {
  repo: string;
  error: string;
}

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
  // forgetHost has the hub forget an offline machine with no working jobs;
  // it throws the hub's refusal for its dialog to show.
  forgetHost: (hostname: string) => Promise<void>;
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
// event); and does what the view asks of them. The app is the hub
// settings' too (useHubGitHub): a change to it there shows here.
export function useDispatch(root: string, live?: DispatchBoard) {
  const api = useMemo(() => createDispatchAPI(root), [root]);
  const [scope, setScope] = useHubSetting("issuesRepo");
  const [preferred, setPreferred] = useHubSetting("issuesAgent");
  const app = useHubGitHub(root);
  const repos = useMemo(() => (app.repos.state === "ready" ? app.repos.value : []), [app.repos]);
  const [issues, setIssues] = useState<ReadonlyMap<string, GitHubIssue[]>>(new Map());
  // The repositories whose issues are being read, and those that failed.
  const [reading, setReading] = useState(false);
  const [issuesFailed, setIssuesFailed] = useState<readonly IssuesFailure[]>([]);
  // The jobs the hub answered an action with, until its stream has them
  // as new.
  const [answered, setAnswered] = useState<ReadonlyMap<string, DispatchJob>>(new Map());
  const streamed = live?.jobs;
  useEffect(() => {
    if (streamed) setAnswered((answers) => pendingAnswers(answers, streamed));
  }, [streamed]);
  // The keys of the issues an action is under way on, and the
  // repositories an issue is being opened in.
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  const [creating, setCreating] = useState<ReadonlySet<string>>(new Set());

  // The repositories whose issues the scope shows.
  const repoNames = useMemo(() => repos.map((repo) => repo.full_name), [repos]);
  // The same list while the names are: the app read again (as the page
  // shows again) doesn't read their issues again.
  const scopedNames = (scope && repoNames.includes(scope) ? [scope] : repoNames.slice(0, recentRepos)).join("\n");
  const scoped = useMemo(() => (scopedNames ? scopedNames.split("\n") : []), [scopedNames]);
  // The latest read of the issues: an earlier one that answers late is
  // dropped.
  const latestRead = useRef(0);
  const readIssues = useCallback(async () => {
    // No repositories (none yet, or the app disconnected): no read under
    // way counts any more.
    const seq = ++latestRead.current;
    if (scoped.length === 0) {
      setReading(false);
      return;
    }
    setReading(true);
    const results = await Promise.allSettled(scoped.map((repo) => api.issues(repo)));
    if (seq !== latestRead.current) return;
    setIssues((previous) => {
      const next = new Map(previous);
      results.forEach((result, i) => {
        if (result.status === "fulfilled") next.set(scoped[i], result.value);
      });
      return next;
    });
    setIssuesFailed(results.flatMap((result, i) => (result.status === "rejected" ? [{repo: scoped[i], error: hubErrorText(result.reason)}] : [])));
    setReading(false);
  }, [api, scoped]);

  useEffect(() => {
    void readIssues();
  }, [readIssues]);

  // The same while the machines are: the cards' suggestions with them.
  const liveHosts = live?.hosts;
  const hosts = useMemo<Load<DispatchHost[]>>(() => (liveHosts ? {state: "ready", value: liveHosts} : {state: "loading"}), [liveHosts]);
  const jobs = useMemo(() => withAnswers(streamed ?? [], answered), [streamed, answered]);

  // A job the hub answered with goes on the board at once, before the
  // stream tells of it.
  const merge = useCallback((job: DispatchJob | undefined) => {
    if (job) setAnswered((all) => new Map(all).set(job.key, job));
  }, []);

  // run does an action on an issue, or opens one, and says how it went; it
  // resolves to what the hub answered, undefined if it didn't go through.
  const run = useCallback(async <T>(on: ActionOn, what: string, done: string, action: () => Promise<T>): Promise<T | undefined> => {
    const [setKeys, key] = "issue" in on ? [setBusy, on.issue] : [setCreating, on.newIn];
    setKeys((keys) => new Set(keys).add(key));
    try {
      const result = await action();
      toast.success(done);
      return result;
    } catch (e) {
      const {title, description} = refusal(e);
      toast.error(title ? `Couldn't ${what}: ${title.toLowerCase()}` : `Couldn't ${what}`, {description});
      return undefined;
    } finally {
      setKeys((keys) => {
        const left = new Set(keys);
        left.delete(key);
        return left;
      });
    }
  }, []);

  // The same actions while the page's API and scope are, so a memoized
  // card doesn't render again for them.
  const actions = useMemo<DispatchActions>(
    () => ({
      assign: async ({repo, number}, agent, host) => {
        const key = jobKey(repo, number);
        const response = await run({issue: key}, `assign ${key}`, `${key} went to ${agent ? agentLabel(agent) : "the machine's default agent"}${host ? ` on ${host}` : ""}`, () =>
          api.dispatch({action: "start", repo, issue: number, agent, host}),
        );
        merge(response?.job);
        return response !== undefined;
      },
      retry: async (job) => {
        const response = await run({issue: job.key}, `retry ${job.key}`, `${agentLabel(job.agent)} starts ${job.key} again`, () =>
          api.dispatch({action: "start", repo: job.repo, issue: job.issue, agent: job.agent}),
        );
        merge(response?.job);
        return response !== undefined;
      },
      switchTo: async (job, agent) => {
        setPreferred(agent);
        const response = await run({issue: job.key}, `switch ${job.key} to ${agentLabel(agent)}`, `${agentLabel(agent)} takes over ${job.key}`, () =>
          api.dispatch({action: "start", repo: job.repo, issue: job.issue, agent}),
        );
        merge(response?.job);
        return response !== undefined;
      },
      stop: async (job) => {
        const response = await run({issue: job.key}, `stop ${job.key}`, `Stopped ${job.key}`, () => api.dispatch({action: "stop", repo: job.repo, issue: job.issue}));
        merge(response?.job);
        return response !== undefined;
      },
      send: async (job, message) => {
        const response = await run({issue: job.key}, `send ${job.key} a message`, `Sent to ${agentLabel(job.agent)}`, () =>
          api.dispatch({action: "send", repo: job.repo, issue: job.issue, message, author: "hub"}),
        );
        merge(response?.job);
        return response !== undefined;
      },
      createIssue: async (request) => {
        const response = await run({newIn: request.repo}, `open the issue in ${request.repo}`, `Opened the issue in ${request.repo}`, () => api.createIssue(request));
        if (!response) return undefined;
        merge(response.job);
        setIssues((all) => new Map(all).set(request.repo, [response.issue, ...(all.get(request.repo) ?? []).filter((i) => i.number !== response.issue.number)]));
        return response.issue;
      },
      refresh: () => void readIssues(),
      // The stream drops the machine once the hub forgot it.
      forgetHost: (hostname: string) => api.forgetHost(hostname),
    }),
    [api, run, merge, setPreferred, readIssues],
  );

  return {
    scope: scope && repoNames.includes(scope) ? scope : "",
    setScope,
    preferred,
    setPreferred,
    github: app.status,
    retryGitHub: app.read,
    repos,
    issues,
    reading,
    issuesFailed,
    hosts,
    jobs,
    busy,
    // An issue is being opened.
    creating: creating.size > 0,
    actions,
  };
}
