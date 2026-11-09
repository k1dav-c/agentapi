"use client";

import {useState} from "react";
import {ArrowRight, Github, Play, Server} from "lucide-react";
import {CopyCommand} from "@/components/copy-command";
import {Button} from "@/components/ui/button";
import {agentLabel, parseIssueRef, type DispatchHost, type GitHubStatus} from "@/lib/hub-dispatch";
import {suggest} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";

const field =
  "h-10 w-full min-w-0 rounded-xl border bg-background px-3.5 text-sm outline-none transition-[border-color,box-shadow] placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/30 dark:bg-input/30";

// The way from no GitHub to an issue at work, as the connect card draws it.
const steps = [
  {title: "Connect", body: "The hub registers its own GitHub App, from a manifest."},
  {title: "Install", body: "Pick the repositories the app may read and push to."},
  {title: "Assign", body: "Open issues show here; one tap hands one to an agent."},
];

// ConnectGitHub is the first thing a hub without GitHub shows: what
// connecting does, in three steps, and the button that starts it. An
// organization's app is a field away.
export function ConnectGitHub({root}: {root: string}) {
  const [org, setOrg] = useState("");
  const [forOrg, setForOrg] = useState(false);
  const connect = `${root}/github/connect${org.trim() ? `?org=${encodeURIComponent(org.trim())}` : ""}`;
  return (
    <section aria-labelledby="connect-github" className="hub-rise overflow-hidden rounded-3xl border bg-card shadow-xs">
      <div className="hub-grid relative border-b px-6 pt-7 pb-6 sm:px-8">
        <span className="flex size-11 items-center justify-center rounded-2xl bg-foreground text-background shadow-md">
          <Github className="size-5" aria-hidden="true" />
        </span>
        <h3 id="connect-github" className="mt-5 text-2xl font-semibold tracking-tight">
          Connect GitHub
        </h3>
        <p className="mt-2 max-w-prose text-[15px] text-pretty text-muted-foreground">
          List your repositories&apos; open issues here, and have agents push branches and open pull requests as the hub&apos;s app, with their spend in a comment. Reviews on the pull
          request go back to the same agent.
        </p>
      </div>
      <ol className="grid gap-px bg-border sm:grid-cols-3">
        {steps.map((step, i) => (
          <li key={step.title} className="bg-card px-6 py-4 sm:px-8">
            <span className="flex items-center gap-2 text-sm font-semibold">
              <span className="flex size-5 items-center justify-center rounded-full bg-foreground/8 text-[11px] tabular-nums">{i + 1}</span>
              {step.title}
            </span>
            <span className="mt-1 block text-[13px] text-muted-foreground">{step.body}</span>
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap items-center gap-3 border-t px-6 py-4 sm:px-8">
        <Button asChild className="h-10 rounded-xl px-4">
          <a href={connect}>
            <Github className="size-4" /> Connect GitHub{org.trim() ? ` for ${org.trim()}` : ""} <ArrowRight className="size-4" />
          </a>
        </Button>
        {forOrg ? (
          <input autoFocus className={cn(field, "w-56 flex-none")} placeholder="Organization" aria-label="Organization" value={org} onChange={(e) => setOrg(e.target.value)} />
        ) : (
          <button type="button" onClick={() => setForOrg(true)} className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
            For an organization?
          </button>
        )}
      </div>
    </section>
  );
}

// InstallApp says the hub's app is installed nowhere yet.
export function InstallApp({status}: {status: GitHubStatus}) {
  return (
    <div role="status" className="flex flex-wrap items-center gap-3 rounded-2xl border border-state-needs/30 bg-state-needs/[0.06] px-4 py-3 text-sm">
      <Github className="size-4 text-state-needs" aria-hidden="true" />
      <p className="min-w-0 flex-1">
        <span className="font-medium">Install {status.slug} on your repositories</span>
        <span className="text-muted-foreground"> to see their issues here.</span>
      </p>
      <Button asChild size="sm" variant="outline">
        <a href={status.install_url} target="_blank" rel="noreferrer">
          Install the app
        </a>
      </Button>
    </div>
  );
}

// NoLaunchers says no machine takes issues yet, and how one does: its
// launcher, installed once on a machine that joined the hub.
export function NoLaunchers({compact = false}: {compact?: boolean}) {
  return (
    <section aria-labelledby="no-launchers" className={cn("rounded-2xl border border-dashed bg-card/50", compact ? "px-4 py-3.5" : "px-5 py-5")}>
      <div className="flex items-start gap-3">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-xl bg-muted">
          <Server className="size-4 text-muted-foreground" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h3 id="no-launchers" className="text-sm font-semibold">
            No machine takes issues yet
          </h3>
          <p className="mt-0.5 text-[13px] text-muted-foreground">
            On a machine that joined the hub, install its launcher once. It reports here within a minute, with the agents it runs and the repositories it has.
          </p>
          <CopyCommand text="agentapi host install --now" />
        </div>
      </div>
    </section>
  );
}

// StartByRef starts an issue by its reference, owner/repo#12 or its URL:
// for a hub without GitHub, whose machines use their own login.
export function StartByRef({hosts, busy, onStart}: {hosts: DispatchHost[]; busy: boolean; onStart: (repo: string, issue: number, agent: string) => Promise<boolean>}) {
  const [ref, setRef] = useState("");
  const parsed = parseIssueRef(ref);
  const pick = parsed ? suggest(hosts, parsed.repo) : undefined;
  return (
    <form
      className="rounded-2xl border bg-card p-4 shadow-xs"
      onSubmit={(event) => {
        event.preventDefault();
        if (parsed) void onStart(parsed.repo, parsed.issue, pick?.agent ?? "").then((ok) => ok && setRef(""));
      }}
    >
      <label htmlFor="issue-ref" className="text-sm font-semibold">
        Start an issue by its reference
      </label>
      <p className="mt-0.5 text-[13px] text-muted-foreground">Without GitHub connected, the machine works with its own GitHub login.</p>
      <div className="mt-3 flex gap-2">
        <input id="issue-ref" className={field} placeholder="owner/repo#12, or the issue's URL" value={ref} onChange={(e) => setRef(e.target.value)} aria-invalid={ref.trim() !== "" && !parsed} />
        <Button type="submit" className="h-10 shrink-0 rounded-xl" disabled={busy || !parsed}>
          <Play className="size-3.5" /> Start
        </Button>
      </div>
      <p className="mt-2 h-4 text-xs text-muted-foreground" aria-live="polite">
        {ref.trim() && !parsed ? "That isn't an issue's reference." : parsed && pick ? `Goes to ${agentLabel(pick.agent)} on ${pick.host.hostname}.` : parsed ? "The hub picks a machine that has the repository or may clone it." : ""}
      </p>
    </form>
  );
}
