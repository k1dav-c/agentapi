"use client";

import {useEffect, useRef, useState} from "react";
import {ArrowRight, ChevronDown, ExternalLink, MessageSquare, RotateCcw, Square} from "lucide-react";
import {Button} from "@/components/ui/button";
import {DropdownMenu, DropdownMenuContent, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import type {HubInstance} from "@/lib/hub";
import {active, agentLabel, prNumber, type DispatchHost, type DispatchJob} from "@/lib/hub-dispatch";
import {activity, formatElapsed} from "@/lib/hub-format";
import {failureText, logPath, switchAgents, type Card, type Option} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";
import {useHubTime} from "../clock";
import {AssignButton, SwitchMenuItems} from "./assign-menu";
import {CopyLine} from "./detail-parts";
import type {DetailActions} from "./issue-detail";
import {JobGlyph, lookLabel, lookTone, type Look} from "./job-glyph";

// The tint of the status block, by look.
const tint: Record<Look, string> = {
  todo: "border-border bg-muted/40",
  starting: "border-state-working/25 bg-state-working/[0.06]",
  working: "border-state-working/25 bg-state-working/[0.06]",
  waiting: "border-state-needs/35 bg-state-needs/[0.07]",
  review: "border-state-ready/30 bg-state-ready/[0.06]",
  failed: "border-state-fault/30 bg-state-fault/[0.06]",
  done: "border-border bg-muted/40",
  stopped: "border-border bg-muted/40",
};

// A change that waits for a second tap: a switch stops the agent at work,
// Stop deletes the branch too.
type Confirming = {kind: "switch"; agent: string} | {kind: "stop"};

// Sentence says in words where the issue is.
function Sentence({card, instance, look}: {card: Card; instance?: HubInstance; look: Look}) {
  const job = card.job;
  const since = (job?.runs ?? []).at(-1)?.started_at || job?.created_at;
  const elapsed = useHubTime((now) => formatElapsed(since, now));
  if (!job) return <>Nobody works on it yet.</>;
  const who = <span className="font-medium text-foreground">{agentLabel(job.agent)}</span>;
  const where = <span className="font-mono text-[0.92em] text-foreground">{job.host}</span>;
  const previous = (job.runs ?? []).at(-2);
  switch (look) {
    case "starting":
      return (
        <>
          {who} is starting on {where}
          {previous && previous.agent !== job.agent ? <>, taking over from {agentLabel(previous.agent)} on the same branch.</> : "."}
        </>
      );
    case "working":
      return (
        <>
          {who} has been working on {where} for <span className="font-mono text-state-working tabular-nums">{elapsed}</span>.
        </>
      );
    case "waiting": {
      const asks = instance ? activity(instance) : undefined;
      return (
        <>
          {who} on {where} waits for you{asks?.label ? `: ${asks.label.toLowerCase()}` : ""}.
        </>
      );
    }
    case "review":
      return (
        <>
          {who} opened pull request #{prNumber(job.pr_url)}. A review that asks for changes, or a comment starting with <code className="rounded bg-muted px-1 font-mono text-[0.9em]">/agent</code>, goes back to it.
        </>
      );
    case "failed":
      return (
        <>
          {who} stopped on {where} and won&apos;t start again by itself.
        </>
      );
    case "stopped":
      return <>{job.error ? `${job.error[0].toUpperCase()}${job.error.slice(1)}.` : "Stopped."}</>;
    default:
      return job.pr_url ? (
        <>
          Done: {who} finished with pull request #{prNumber(job.pr_url)}.
        </>
      ) : (
        <>Done.</>
      );
  }
}

// ConfirmStrip asks for the second tap, in place of the status's actions:
// Cancel has the focus, and Esc is Cancel too (not Back for the page or
// the sheet).
function ConfirmStrip({confirming, job, busy, onCancel, onConfirm}: {confirming: Confirming; job: DispatchJob; busy: boolean; onCancel: () => void; onConfirm: () => void}) {
  return (
    <div
      role="alertdialog"
      aria-label="Confirm"
      className="hub-rise mt-4 rounded-xl border bg-background p-3.5 shadow-sm"
      onKeyDown={(event) => {
        if (event.key !== "Escape") return;
        event.stopPropagation();
        onCancel();
      }}
    >
      <p className="text-sm text-pretty">
        {confirming.kind === "stop" ? (
          <>
            Stop {agentLabel(job.agent)}? Its worktree and the branch <code className="font-mono text-[0.92em]">agent/issue-{job.issue}</code> are deleted, on {job.host} and on GitHub.
          </>
        ) : (
          <>
            Stop {agentLabel(job.agent)} and have <span className="font-medium">{agentLabel(confirming.agent)}</span> take over on {job.host}? The branch, the worktree and the pull request stay.
          </>
        )}
      </p>
      <div className="mt-3 flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel} autoFocus>
          Cancel
        </Button>
        <Button size="sm" variant={confirming.kind === "stop" ? "destructive" : "default"} disabled={busy} onClick={onConfirm}>
          {confirming.kind === "stop" ? "Stop and delete the branch" : `Switch to ${agentLabel(confirming.agent)}`}
        </Button>
      </div>
    </div>
  );
}

// Status is the block at the top of the detail: the issue's state in a
// word and a sentence, what its agent does or asks now, and what you can
// do about it, the likely thing first.
export function Status({
  card,
  job,
  look,
  hosts,
  instance,
  suggested,
  disabledReason,
  busy,
  actions,
}: {
  card: Card;
  job?: DispatchJob;
  look: Look;
  hosts: DispatchHost[];
  instance?: HubInstance;
  suggested?: Option;
  disabledReason?: string;
  busy: boolean;
  actions: DetailActions;
}) {
  const [confirming, setConfirming] = useState<Confirming | null>(null);
  const section = useRef<HTMLElement>(null);
  const stopButton = useRef<HTMLButtonElement>(null);
  const switchButton = useRef<HTMLButtonElement>(null);
  // The confirmation gone, by an answer or a change from elsewhere, the
  // focus it had goes back to the button that asked for it (or, gone too,
  // the status's first), not to the page's body.
  const asked = useRef<Confirming["kind"] | null>(null);
  useEffect(() => {
    if (confirming) {
      asked.current = confirming.kind;
      return;
    }
    const kind = asked.current;
    asked.current = null;
    if (!kind || (document.activeElement && document.activeElement !== document.body)) return;
    const back = (kind === "stop" ? stopButton : switchButton).current;
    (back && !back.disabled ? back : section.current?.querySelector<HTMLElement>("button:not(:disabled), a[href]"))?.focus();
  }, [confirming]);
  const log = logPath(job?.error);
  const others = job ? switchAgents(hosts, job) : [];
  const live = job && instance && (look === "working" || look === "waiting") ? activity(instance) : undefined;
  // A confirmation is for the job as it was: a change from elsewhere ends it.
  const state = job?.state;
  useEffect(() => setConfirming(null), [state]);

  const confirm = confirming && job && (
    <ConfirmStrip
      confirming={confirming}
      job={job}
      busy={busy}
      onCancel={() => setConfirming(null)}
      onConfirm={() => {
        if (confirming.kind === "stop") void actions.stop(job);
        else void actions.switchTo(job, confirming.agent);
        setConfirming(null);
      }}
    />
  );

  const switchMenu = others.length > 0 && job && (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button ref={switchButton} size="sm" variant="ghost" disabled={busy} className="gap-1 text-muted-foreground hover:text-foreground">
          Switch agent <ChevronDown className="size-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56 rounded-xl p-1">
        <p className="px-2.5 pt-1.5 pb-1 text-[11px] text-muted-foreground">Same branch, on {job.host}</p>
        <SwitchMenuItems agents={others} onPick={(agent) => setConfirming({kind: "switch", agent})} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
  const stop = job && active(job) && (
    <Button
      ref={stopButton}
      size="sm"
      variant="ghost"
      disabled={busy}
      className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
      onClick={() => setConfirming({kind: "stop"})}
    >
      <Square className="size-3.5" /> Stop
    </Button>
  );
  const chat = job?.instance_id && active(job) && look !== "failed" && (
    <Button size="sm" variant={look === "working" || look === "starting" ? "default" : "outline"} onClick={() => actions.openChat(job.instance_id)}>
      <MessageSquare className="size-3.5" /> Open chat
    </Button>
  );

  return (
    <section ref={section} aria-label="Status" className={cn("rounded-2xl border p-4", tint[look])}>
      <p className={cn("flex items-center gap-2 text-xs font-semibold tracking-[0.08em] uppercase", lookTone[look])}>
        <JobGlyph look={look} className="size-4" />
        {lookLabel[look]}
      </p>
      <p className="mt-2 text-[15px] leading-relaxed text-pretty text-muted-foreground">
        <Sentence card={card} instance={instance} look={look} />
      </p>
      {live && (
        <p className={cn("mt-3 rounded-lg bg-background/70 px-3 py-2 text-[13px]", look === "waiting" ? "font-mono text-foreground" : "text-muted-foreground")}>
          {look === "working" && <span className="hub-shimmer mb-2 block h-[3px] rounded-full bg-state-working/12" aria-hidden="true" />}
          {live.text}
        </p>
      )}
      {job?.error && look === "failed" && (
        <div className="mt-3 overflow-hidden rounded-lg border border-state-fault/20 bg-background/70">
          <p className="px-3 pt-2.5 pb-0.5 text-[13px] text-state-fault [overflow-wrap:anywhere]">{failureText(job.error)}</p>
          {log && <CopyLine label="Log" value={log} className="py-1 pr-1.5 pl-3 [&_dt]:w-12" />}
        </div>
      )}

      {confirm || (
        <div className="mt-4 flex flex-wrap items-center gap-2">
          {!job || look === "done" || look === "stopped" ? (
            <AssignButton
              hosts={hosts}
              repo={card.repo}
              suggested={suggested}
              disabledReason={disabledReason}
              busy={busy}
              label={job ? "Start again" : "Assign"}
              onAssign={(agent, host, picked) => actions.assignCard(card, agent, host, picked)}
            />
          ) : look === "failed" ? (
            <>
              <Button size="sm" disabled={busy} className="max-sm:h-10 max-sm:w-full" onClick={() => void actions.retry(job)}>
                <RotateCcw className="size-3.5" /> Retry with {agentLabel(job.agent)}
              </Button>
            </>
          ) : look === "waiting" ? (
            <>
              <Button size="sm" className="bg-state-needs text-background hover:bg-state-needs/90 max-sm:h-10 max-sm:w-full" onClick={() => actions.openChat(job.instance_id)}>
                Answer in the chat <ArrowRight className="size-3.5" />
              </Button>
            </>
          ) : look === "review" ? (
            <>
              {job.pr_url && (
                <Button size="sm" asChild className="max-sm:h-10 max-sm:w-full">
                  <a href={job.pr_url} target="_blank" rel="noreferrer">
                    Review #{prNumber(job.pr_url)} <ExternalLink className="size-3.5" />
                  </a>
                </Button>
              )}
              {chat}
            </>
          ) : (
            <>
              {chat}
            </>
          )}
        </div>
      )}
      {!confirm && (switchMenu || stop) && look !== "done" && look !== "stopped" && (
        <div className="mt-3 -mb-1 flex items-center gap-1 border-t border-current/10 pt-2 text-muted-foreground [&>button]:-ml-1">
          {switchMenu}
          <span className="flex-1" />
          {stop}
        </div>
      )}
      {!job && suggested && (
        <p className="mt-2.5 text-xs text-muted-foreground">
          Goes to {agentLabel(suggested.agent)} on <span className="font-mono">{suggested.host.hostname}</span>, which {suggested.hasRepo ? "has the repository" : "clones it"} and runs {suggested.host.active} of {suggested.host.max_jobs} jobs. The arrow picks another.
        </p>
      )}
      {!job && !suggested && disabledReason && <p className="mt-2.5 text-xs text-muted-foreground">{disabledReason}.</p>}
    </section>
  );
}
