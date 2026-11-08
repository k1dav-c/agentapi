"use client";

import {useCallback, useEffect, useMemo, useState} from "react";
import {ExternalLink, Github, MessageSquare, Play, RefreshCw, RotateCcw, Square} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {hubErrorText} from "@/lib/hub-api";
import {
  active,
  agentLabel,
  createDispatchAPI,
  hostAgents,
  hostsFor,
  jobCost,
  jobKey,
  jobStateLabel,
  parseIssueRef,
  prNumber,
  type CreateIssueRequest,
  type DispatchHost,
  type DispatchJob,
  type GitHubIssue,
  type GitHubRepo,
  type GitHubStatus,
} from "@/lib/hub-dispatch";
import {useHubSetting} from "@/lib/hub-settings";
import {formatUSD} from "@/lib/hub-spend";
import {cn} from "@/lib/utils";
import {fieldClass, sectionClass} from "./dialogs";

// How often an open dialog reads the machines and the jobs again.
const refreshEvery = 10_000;

const selectClass = cn(fieldClass, "h-9 w-auto min-w-0 px-2.5");

// AgentSelect picks an agent: the machine's default, or one the machines
// online run.
function AgentSelect({agents, value, onChange, label, includeDefault = true}: {agents: string[]; value: string; onChange: (agent: string) => void; label: string; includeDefault?: boolean}) {
  return (
    <select aria-label={label} className={selectClass} value={value} onChange={(e) => onChange(e.target.value)}>
      {includeDefault && <option value="">{agentLabel("")}</option>}
      {agents.map((agent) => (
        <option key={agent} value={agent}>
          {agentLabel(agent)}
        </option>
      ))}
    </select>
  );
}

// HostSelect picks a machine that can start the issue, or lets the hub
// pick.
function HostSelect({hosts, value, onChange}: {hosts: DispatchHost[]; value: string; onChange: (host: string) => void}) {
  return (
    <select aria-label="Machine" className={selectClass} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Any machine</option>
      {hosts.map((host) => (
        <option key={host.hostname} value={host.hostname}>
          {host.hostname}
          {(host.repos ?? []).length > 0 ? "" : " (clones)"}
        </option>
      ))}
    </select>
  );
}

const stateTone: Record<DispatchJob["state"], string> = {
  starting: "bg-state-needs/15 text-state-needs",
  working: "bg-state-working/15 text-state-working",
  review: "bg-state-ready/15 text-state-ready",
  failed: "bg-destructive/15 text-destructive",
  done: "bg-muted text-muted-foreground",
  stopped: "bg-muted text-muted-foreground",
};

// JobSummary is what a job is at: its state, agent and machine, its pull
// request and spend, and why it failed.
export function JobSummary({job, onOpen}: {job: DispatchJob; onOpen?: (instanceID: string) => void}) {
  const pr = prNumber(job.pr_url);
  const cost = jobCost(job);
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
      <span className={cn("rounded-full px-2 py-0.5 font-medium", stateTone[job.state])}>{jobStateLabel(job.state)}</span>
      <span>
        {agentLabel(job.agent)} on <span className="font-mono">{job.host}</span>
      </span>
      {pr !== undefined && (
        <a className="inline-flex items-center gap-1 underline-offset-2 hover:underline" href={job.pr_url} target="_blank" rel="noreferrer">
          PR #{pr} <ExternalLink className="size-3" />
        </a>
      )}
      {cost > 0 && <span className="tabular-nums" title="API-equivalent spend of all its runs">{formatUSD(cost)}</span>}
      {(job.runs ?? []).length > 1 && <span>{(job.runs ?? []).length} runs</span>}
      {active(job) && job.state !== "failed" && onOpen && job.instance_id && (
        <button type="button" className="inline-flex items-center gap-1 text-foreground underline-offset-2 hover:underline" onClick={() => onOpen(job.instance_id)}>
          <MessageSquare className="size-3" /> Open chat
        </button>
      )}
      {job.error && <span className="w-full text-destructive">{job.error}</span>}
    </div>
  );
}

// The actions on an issue; each resolves to whether it went through.
export interface IssueActions {
  start: (repo: string, issue: number, agent: string, host: string) => Promise<boolean>;
  send: (job: DispatchJob, message: string) => Promise<boolean>;
  stop: (job: DispatchJob) => Promise<boolean>;
  open: (instanceID: string) => void;
}

// A change that waits for a second click: switching agents stops the
// agent that works, and Stop deletes the job's branch too.
type Confirming = {kind: "switch"; agent: string} | {kind: "stop"};

// IssueItem is an issue: its title and labels, the job on it, and Assign;
// for work that goes on, a message to its agent, another agent and Stop;
// for a failed job, Retry.
export function IssueItem({
  repo,
  issue,
  job,
  hosts,
  actions,
  busy,
  initialConfirm = null,
}: {
  repo: string;
  issue: Pick<GitHubIssue, "number" | "title" | "html_url" | "labels">;
  job?: DispatchJob;
  hosts: DispatchHost[];
  actions: IssueActions;
  busy: boolean;
  initialConfirm?: Confirming | null;
}) {
  const working = job !== undefined && active(job);
  const failed = job?.state === "failed";
  const [agent, setAgent] = useState("");
  const [host, setHost] = useState("");
  const [message, setMessage] = useState("");
  const [switchTo, setSwitchTo] = useState("");
  const [confirming, setConfirming] = useState<Confirming | null>(initialConfirm);
  const agents = useMemo(() => hostAgents(hosts), [hosts]);
  const candidates = useMemo(() => hostsFor(hosts, repo, agent), [hosts, repo, agent]);
  const picked = candidates.some((h) => h.hostname === host) ? host : "";
  const others = agents.filter((a) => a !== job?.agent);
  return (
    <li className="space-y-2 rounded-xl border bg-card px-3.5 py-3">
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="shrink-0 font-mono text-xs text-muted-foreground">#{issue.number}</span>
        <a className="min-w-0 flex-1 truncate text-sm font-medium underline-offset-2 hover:underline" href={issue.html_url} target="_blank" rel="noreferrer">
          {issue.title}
        </a>
        {(issue.labels ?? []).map((label) => (
          <span key={label.name} className="shrink-0 rounded-full border px-1.5 text-[10px] text-muted-foreground">
            {label.name}
          </span>
        ))}
      </div>
      {job && <JobSummary job={job} onOpen={actions.open} />}
      {working && confirming ? (
        <div role="group" aria-label="Confirm" className="flex flex-wrap items-center gap-2 rounded-lg bg-muted/60 px-3 py-2 text-sm">
          <span className="flex-1">
            {confirming.kind === "stop"
              ? `Stop ${agentLabel(job.agent)}? Its worktree and the branch agent/issue-${issue.number} are deleted, here and on GitHub.`
              : `Stop ${agentLabel(job.agent)} and have ${agentLabel(confirming.agent)} take over on the same branch?`}
          </span>
          <Button size="sm" variant="ghost" onClick={() => setConfirming(null)}>
            Cancel
          </Button>
          <Button
            size="sm"
            variant={confirming.kind === "stop" ? "destructive" : "default"}
            disabled={busy}
            onClick={() => {
              const done = confirming.kind === "stop" ? actions.stop(job) : actions.start(repo, issue.number, confirming.agent, "");
              setConfirming(null);
              setSwitchTo("");
              void done;
            }}
          >
            {confirming.kind === "stop" ? "Stop" : `Switch to ${agentLabel(confirming.agent)}`}
          </Button>
        </div>
      ) : working ? (
        <div className="flex flex-wrap items-center gap-2">
          {failed ? (
            <Button size="sm" disabled={busy} onClick={() => void actions.start(repo, issue.number, job.agent, "")}>
              <RotateCcw className="size-3.5" /> Retry with {agentLabel(job.agent)}
            </Button>
          ) : (
            <form
              className="flex min-w-0 flex-1 gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                if (!message.trim()) return;
                void actions.send(job, message.trim()).then((sent) => sent && setMessage(""));
              }}
            >
              <input className={cn(fieldClass, "h-9")} placeholder={`Message ${agentLabel(job.agent)}`} aria-label="Message to the agent" value={message} onChange={(e) => setMessage(e.target.value)} />
              <Button type="submit" size="sm" variant="outline" disabled={busy || !message.trim()}>
                Send
              </Button>
            </form>
          )}
          {others.length > 0 && (
            <>
              <AgentSelect label="Another agent" agents={others} value={switchTo} onChange={setSwitchTo} includeDefault={false} />
              <Button size="sm" variant="outline" disabled={busy || !switchTo} onClick={() => setConfirming({kind: "switch", agent: switchTo})}>
                Switch
              </Button>
            </>
          )}
          <Button size="sm" variant="outline" disabled={busy} onClick={() => setConfirming({kind: "stop"})}>
            <Square className="size-3.5" /> Stop
          </Button>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <AgentSelect label="Agent" agents={agents} value={agent} onChange={setAgent} />
          <HostSelect hosts={candidates} value={picked} onChange={setHost} />
          <Button size="sm" disabled={busy || candidates.length === 0} onClick={() => void actions.start(repo, issue.number, agent, picked)}>
            <Play className="size-3.5" /> {job ? "Start again" : "Assign"}
          </Button>
          {candidates.length === 0 && <span className="text-xs text-muted-foreground">No machine online runs it with room.</span>}
        </div>
      )}
    </li>
  );
}

// GitHubSection says whether the hub has its GitHub App: connecting it
// registers the app from a manifest on GitHub, which then sends the
// browser back here.
export function GitHubSection({root, status}: {root: string; status: GitHubStatus | null}) {
  const [org, setOrg] = useState("");
  if (status?.connected) {
    const accounts = (status.installations ?? []).map((i) => i.account).join(", ");
    return (
      <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
        <Github className="size-3.5" />
        <span>
          GitHub App <span className="font-mono text-foreground">{status.slug}</span>
          {accounts ? ` on ${accounts}` : ", not installed yet"}.
        </span>
        <a className="underline underline-offset-2" href={status.install_url} target="_blank" rel="noreferrer">
          {accounts ? "Choose repositories" : "Install it"}
        </a>
      </p>
    );
  }
  const connect = `${root}/github/connect${org.trim() ? `?org=${encodeURIComponent(org.trim())}` : ""}`;
  return (
    <div className="space-y-2 rounded-xl border border-dashed px-3.5 py-3 text-sm">
      <p>
        <span className="font-medium">Connect GitHub</span> to list issues here, have agents push and open pull requests as the hub&apos;s app, and comment their spend.
        Without it, work starts with each machine&apos;s own GitHub login.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <input className={cn(fieldClass, "h-9 w-48")} placeholder="Organization (optional)" aria-label="Organization" value={org} onChange={(e) => setOrg(e.target.value)} />
        <Button asChild size="sm">
          <a href={connect}>
            <Github className="size-3.5" /> Connect GitHub
          </a>
        </Button>
      </div>
    </div>
  );
}

// MachinesLine lists the machines whose launchers report, and their jobs.
export function MachinesLine({hosts}: {hosts: DispatchHost[]}) {
  if (hosts.length === 0) {
    return <p className="text-xs text-muted-foreground">No machine&apos;s launcher reports to the hub: run agentapi host install on a machine that joined it.</p>;
  }
  return (
    <ul className="flex flex-wrap gap-1.5 text-xs">
      {hosts.map((host) => (
        <li key={host.hostname} className={cn("rounded-full border px-2 py-0.5", !host.online && "opacity-50")} title={(host.repos ?? []).join("\n")}>
          <span className="font-mono">{host.hostname}</span> · {host.online ? `${host.active}/${host.max_jobs} jobs` : "offline"} ·{" "}
          {(host.agents ?? []).map(agentLabel).join(", ") || "no agent"}
        </li>
      ))}
    </ul>
  );
}

// IssuesDialog dispatches GitHub issues to the machines: the hub's GitHub
// App, a repository's open issues with the work on each, a new issue, and
// for a hub without GitHub, an issue by its reference.
export function IssuesDialog({root, open, onOpenChange, onOpen}: {root: string; open: boolean; onOpenChange: (open: boolean) => void; onOpen: (instanceID: string) => void}) {
  const api = useMemo(() => createDispatchAPI(root), [root]);
  const [status, setStatus] = useState<GitHubStatus | null>(null);
  const [hosts, setHosts] = useState<DispatchHost[]>([]);
  const [jobs, setJobs] = useState<DispatchJob[]>([]);
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  const [repo, setRepo] = useHubSetting("issuesRepo");
  const [issues, setIssues] = useState<GitHubIssue[] | null>(null);
  // The keys of the issues an action is under way on.
  const [busy, setBusy] = useState<ReadonlySet<string>>(new Set());
  // error is a load's that failed; pollError the last refresh's, until one
  // succeeds.
  const [error, setError] = useState("");
  const [pollError, setPollError] = useState("");

  const refresh = useCallback(async () => {
    try {
      const [h, j] = await Promise.all([api.hosts(), api.jobs()]);
      setHosts(h);
      setJobs(j);
      setPollError("");
    } catch (e) {
      setPollError(hubErrorText(e));
    }
  }, [api]);

  useEffect(() => {
    if (!open) return;
    setError("");
    void refresh();
    void api
      .github()
      .then(async (s) => {
        setStatus(s);
        if (s.connected) setRepos(await api.repos());
      })
      .catch((e) => setError(hubErrorText(e)));
    const timer = setInterval(() => void refresh(), refreshEvery);
    return () => clearInterval(timer);
  }, [open, api, refresh]);

  const loadIssues = useCallback(
    async (name: string) => {
      setIssues(null);
      if (!name) return;
      try {
        setIssues(await api.issues(name));
      } catch (e) {
        setError(hubErrorText(e));
        setIssues([]);
      }
    },
    [api],
  );
  useEffect(() => {
    if (open && status?.connected && repo) void loadIssues(repo);
  }, [open, status?.connected, repo, loadIssues]);

  // run does an action on the issue of key, says how it went, and reads
  // the jobs again; it resolves to whether it went through.
  const run = async (key: string, what: string, action: () => Promise<unknown>): Promise<boolean> => {
    setBusy((keys) => new Set(keys).add(key));
    try {
      await action();
      toast.success(what);
      return true;
    } catch (e) {
      toast.error(`Couldn't ${what.charAt(0).toLowerCase()}${what.slice(1)}`, {description: hubErrorText(e)});
      return false;
    } finally {
      setBusy((keys) => {
        const left = new Set(keys);
        left.delete(key);
        return left;
      });
      void refresh();
    }
  };
  const actions: IssueActions = {
    start: (r, issue, agent, host) =>
      run(jobKey(r, issue), `Start ${jobKey(r, issue)}${agent ? ` with ${agentLabel(agent)}` : ""}`, () => api.dispatch({action: "start", repo: r, issue, agent, host})),
    send: (job, message) => run(job.key, `Send ${job.key} a message`, () => api.dispatch({action: "send", repo: job.repo, issue: job.issue, message, author: "hub"})),
    stop: (job) => run(job.key, `Stop ${job.key}`, () => api.dispatch({action: "stop", repo: job.repo, issue: job.issue})),
    open: (id) => {
      onOpenChange(false);
      onOpen(id);
    },
  };
  const createIssue = (request: CreateIssueRequest) => run(`new ${request.repo}`, `Open the issue in ${request.repo}`, () => api.createIssue(request));
  const byKey = useMemo(() => new Map(jobs.map((job) => [job.key, job])), [jobs]);
  const others = jobs.filter((job) => active(job) && (job.repo !== repo || !(issues ?? []).some((i) => i.number === job.issue)));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Issues</DialogTitle>
          <DialogDescription>Hand a GitHub issue to an agent on one of your machines; its pull request&apos;s reviews go back to the same agent.</DialogDescription>
        </DialogHeader>
        <section className="space-y-2">
          <h3 className={sectionClass}>Machines</h3>
          <MachinesLine hosts={hosts} />
        </section>
        <GitHubSection root={root} status={status} />
        {(error || pollError) && (
          <p role="alert" className="text-sm text-destructive">
            {error || pollError}
          </p>
        )}
        {status?.connected ? (
          <section className="space-y-3">
            <div className="flex items-center gap-2">
              <select aria-label="Repository" className={cn(fieldClass, "h-9")} value={repo} onChange={(e) => setRepo(e.target.value)}>
                <option value="">Choose a repository…</option>
                {repos.map((r) => (
                  <option key={r.full_name} value={r.full_name}>
                    {r.full_name}
                  </option>
                ))}
              </select>
              <Button variant="ghost" size="icon" aria-label="Reload the issues" title="Reload the issues" disabled={!repo} onClick={() => void loadIssues(repo)}>
                <RefreshCw className="size-4" />
              </Button>
            </div>
            {repo && (
              <NewIssue
                key={repo}
                repo={repo}
                hosts={hosts}
                busy={busy.has(`new ${repo}`)}
                onCreate={async (request) => {
                  const created = await createIssue(request);
                  if (created) void loadIssues(repo);
                  return created;
                }}
              />
            )}
            {repo && issues === null && <p className="text-sm text-muted-foreground">Reading the issues…</p>}
            {repo && issues?.length === 0 && <p className="text-sm text-muted-foreground">No open issue.</p>}
            <ul className="space-y-2">
              {(issues ?? []).map((issue) => (
                <IssueItem
                  key={jobKey(repo, issue.number)}
                  repo={repo}
                  issue={issue}
                  job={byKey.get(jobKey(repo, issue.number))}
                  hosts={hosts}
                  actions={actions}
                  busy={busy.has(jobKey(repo, issue.number))}
                />
              ))}
            </ul>
          </section>
        ) : (
          <ManualStart hosts={hosts} busy={busy.size > 0} onStart={actions.start} />
        )}
        {others.length > 0 && (
          <section className="space-y-2">
            <h3 className={sectionClass}>Other work going on</h3>
            <ul className="space-y-2">
              {others.map((job) => (
                <IssueItem
                  key={job.key}
                  repo={job.repo}
                  issue={{number: job.issue, title: job.title || job.key, html_url: job.issue_url, labels: []}}
                  job={job}
                  hosts={hosts}
                  actions={actions}
                  busy={busy.has(job.key)}
                />
              ))}
            </ul>
          </section>
        )}
      </DialogContent>
    </Dialog>
  );
}

// ManualStart starts an issue by its reference, for a hub without GitHub.
export function ManualStart({hosts, busy, onStart}: {hosts: DispatchHost[]; busy: boolean; onStart: IssueActions["start"]}) {
  const [ref, setRef] = useState("");
  const [agent, setAgent] = useState("");
  const parsed = parseIssueRef(ref);
  return (
    <form
      className="space-y-2"
      onSubmit={(event) => {
        event.preventDefault();
        if (parsed) void onStart(parsed.repo, parsed.issue, agent, "");
      }}
    >
      <h3 className={sectionClass}>Start an issue</h3>
      <div className="flex flex-wrap gap-2">
        <input className={cn(fieldClass, "h-9 min-w-56 flex-1")} placeholder="owner/repo#12 or the issue's URL" aria-label="Issue" value={ref} onChange={(e) => setRef(e.target.value)} />
        <AgentSelect label="Agent" agents={hostAgents(hosts)} value={agent} onChange={setAgent} />
        <Button type="submit" size="sm" className="h-9" disabled={busy || !parsed}>
          <Play className="size-3.5" /> Start
        </Button>
      </div>
    </form>
  );
}

// NewIssue opens an issue in repo as the hub's app, and starts it at once
// when Assign is on; what was typed stays when that fails.
function NewIssue({repo, hosts, busy, onCreate}: {repo: string; hosts: DispatchHost[]; busy: boolean; onCreate: (request: CreateIssueRequest) => Promise<boolean>}) {
  const [shown, setShown] = useState(false);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [assign, setAssign] = useState(true);
  const [agent, setAgent] = useState("");
  if (!shown) {
    return (
      <Button variant="outline" size="sm" onClick={() => setShown(true)}>
        New issue
      </Button>
    );
  }
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (await onCreate({repo, title: title.trim(), body, assign: assign ? {agent} : undefined})) {
      setTitle("");
      setBody("");
      setShown(false);
    }
  };
  return (
    <form className="space-y-2 rounded-xl border bg-card px-3.5 py-3" onSubmit={(e) => void submit(e)}>
      <input className={cn(fieldClass, "h-9")} placeholder="Title" aria-label="Title" value={title} onChange={(e) => setTitle(e.target.value)} required />
      <textarea className={cn(fieldClass, "h-28 py-2")} placeholder="What should change, and how to tell it's done" aria-label="Description" value={body} onChange={(e) => setBody(e.target.value)} />
      <div className="flex flex-wrap items-center gap-2">
        <label className="flex items-center gap-1.5 text-sm">
          <input type="checkbox" checked={assign} onChange={(e) => setAssign(e.target.checked)} /> Assign to
        </label>
        <AgentSelect label="Agent" agents={hostAgents(hosts)} value={agent} onChange={setAgent} />
        <span className="flex-1" />
        <Button type="button" variant="ghost" size="sm" onClick={() => setShown(false)}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={busy || !title.trim()}>
          Open issue
        </Button>
      </div>
    </form>
  );
}
