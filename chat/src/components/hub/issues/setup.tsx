"use client";

import {useState} from "react";
import {ArrowRight, Github, Play, Server} from "lucide-react";
import {CopyCommand} from "@/components/copy-command";
import {Button} from "@/components/ui/button";
import {agentLabel, parseIssueRef, type DispatchHost, type GitHubStatus} from "@/lib/hub-dispatch";
import {connectURL} from "@/lib/hub-github";
import {suggest} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";
import {cardFieldClass} from "../styles";

// The way from no GitHub to an issue at work, as the connect card draws it.
const steps = [
  {title: "Connect", body: "The hub registers its own GitHub App, from a manifest."},
  {title: "Install", body: "Pick the repositories the app may read and push to."},
  {title: "Assign", body: "Open issues show here; one tap hands one to an agent."},
];

// ConnectGitHub is the first thing a hub without GitHub shows: what
// connecting does, in three steps, and the button that starts it. An
// organization's app is a field away. compact is the hub settings' own,
// in a dialog's width.
export function ConnectGitHub({root, compact = false}: {root: string; compact?: boolean}) {
  const [org, setOrg] = useState("");
  const [forOrg, setForOrg] = useState(false);
  const connect = connectURL(root, org);
  const pad = compact ? "px-5" : "px-6 sm:px-8";
  // Under the settings' own heading, a level down.
  const Heading = compact ? "h4" : "h3";
  return (
    <section aria-labelledby="connect-github" className={cn("hub-rise overflow-hidden border bg-card shadow-xs", compact ? "rounded-2xl" : "rounded-3xl")}>
      <div className={cn("hub-grid relative border-b", pad, compact ? "pt-5 pb-4" : "pt-7 pb-6")}>
        <span className={cn("flex items-center justify-center bg-foreground text-background shadow-md", compact ? "size-9 rounded-xl" : "size-11 rounded-2xl")}>
          <Github className={compact ? "size-4" : "size-5"} aria-hidden="true" />
        </span>
        <Heading id="connect-github" className={cn("font-semibold tracking-tight", compact ? "mt-4 text-lg" : "mt-5 text-2xl")}>
          Connect GitHub
        </Heading>
        <p className={cn("mt-2 max-w-prose text-pretty text-muted-foreground", compact ? "text-sm" : "text-[15px]")}>
          List your repositories&apos; open issues here, and have agents push branches and open pull requests as the hub&apos;s app, with their spend in a comment. Reviews on the pull
          request go back to the same agent.
        </p>
      </div>
      <ol className="grid gap-px bg-border sm:grid-cols-3">
        {steps.map((step, i) => (
          <li key={step.title} className={cn("bg-card", pad, compact ? "py-3.5" : "py-4")}>
            <span className="flex items-center gap-2 text-sm font-semibold">
              <span className="flex size-5 items-center justify-center rounded-full bg-foreground/8 text-[11px] tabular-nums">{i + 1}</span>
              {step.title}
            </span>
            <span className="mt-1 block text-[13px] text-muted-foreground">{step.body}</span>
          </li>
        ))}
      </ol>
      <div className={cn("flex flex-wrap items-center gap-3 border-t py-4", pad)}>
        <Button asChild className="h-10 rounded-xl px-4">
          <a href={connect}>
            <Github className="size-4" /> Connect GitHub{org.trim() ? ` for ${org.trim()}` : ""} <ArrowRight className="size-4" />
          </a>
        </Button>
        {forOrg ? (
          <input autoFocus className={cn(cardFieldClass, "w-56 max-w-full flex-none")} placeholder="Organization" aria-label="Organization" value={org} onChange={(e) => setOrg(e.target.value)} />
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
        <input id="issue-ref" className={cardFieldClass} placeholder="owner/repo#12, or the issue's URL" value={ref} onChange={(e) => setRef(e.target.value)} aria-invalid={ref.trim() !== "" && !parsed} />
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
