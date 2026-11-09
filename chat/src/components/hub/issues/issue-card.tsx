"use client";

import {memo} from "react";
import {ArrowRight, ExternalLink, GitPullRequest, MessageSquare, RotateCcw} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import type {HubInstance} from "@/lib/hub";
import {agentLabel, jobCost, prNumber, type DispatchHost} from "@/lib/hub-dispatch";
import {activity, formatAgo, formatElapsed} from "@/lib/hub-format";
import {failureText, repoName, type Card, type Option} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";
import {useHubTime} from "../clock";
import {AssignButton} from "./assign-menu";
import {JobGlyph, lookLabel, lookOf, lookRing, lookTone} from "./job-glyph";
import {Money} from "./money";

export interface CardActions {
  select: (key: string) => void;
  assign: (card: Card, agent: string, host: string, picked: boolean) => void;
  retry: (card: Card) => void;
  openChat: (instanceID: string) => void;
}

const dot = <span aria-hidden="true" className="text-muted-foreground/40">·</span>;

// Age is how long the card's job has been at it: running time while it
// works, how long ago it changed otherwise.
function Age({card}: {card: Card}) {
  const job = card.job;
  const running = job && (job.state === "working" || job.state === "starting");
  const since = running ? (job.runs ?? []).at(-1)?.started_at || job.created_at : job?.updated_at;
  const text = useHubTime((now) => (running ? formatElapsed(since, now) : formatAgo(since, now)));
  if (!job || !text) return null;
  return <span className={cn("shrink-0 tabular-nums", running && "font-mono text-state-working")}>{running ? text : text === "now" ? "just now" : `${text} ago`}</span>;
}

// Live is what the job's agent does now, from the hub's stream: what it
// asks you, or the task it works on, with the scan that runs while it does.
function Live({card, instance}: {card: Card; instance?: HubInstance}) {
  const job = card.job;
  if (!job) return null;
  if (job.state === "failed") {
    return <p className="mt-1.5 line-clamp-2 text-[13px] leading-snug text-state-fault [overflow-wrap:anywhere]">{failureText(job.error)}</p>;
  }
  if (job.state === "starting") {
    return (
      <div className="mt-2.5">
        <span className="hub-shimmer block h-[3px] rounded-full bg-state-working/12" aria-hidden="true" />
        <p className="mt-1.5 text-[13px] text-muted-foreground">Starting {agentLabel(job.agent)} on its worktree…</p>
      </div>
    );
  }
  if (!instance) return null;
  const doing = activity(instance);
  if (card.waiting) {
    return (
      <p className="mt-1.5 flex min-w-0 items-baseline gap-1.5 text-[13px]">
        <span className="shrink-0 font-medium text-state-needs">{doing.label}</span>
        <span className="min-w-0 truncate font-mono text-[12px] text-foreground/80">{doing.text}</span>
      </p>
    );
  }
  if (job.state === "working") {
    return (
      <div className="mt-2.5">
        <span className="hub-shimmer block h-[3px] rounded-full bg-state-working/12" aria-hidden="true" />
        <p className="mt-1.5 truncate text-[13px] text-muted-foreground">{doing.text}</p>
      </div>
    );
  }
  return null;
}

// Meta is who works on the card's issue and what came of it: the agent on
// its machine, the pull request, the spend and the runs; to do, its
// labels.
function Meta({card, showRepo}: {card: Card; showRepo: boolean}) {
  const job = card.job;
  const pr = prNumber(job?.pr_url);
  const cost = job ? jobCost(job) : 0;
  const runs = (job?.runs ?? []).length;
  return (
    <p className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
      {showRepo && (
        <>
          <span className="font-mono">{repoName(card.repo)}</span>
          {(job || card.labels.length > 0) && dot}
        </>
      )}
      {job ? (
        <>
          <span className="inline-flex items-center gap-1">
            <AgentMark agentType={job.agent} className="size-3" />
            {agentLabel(job.agent)}
          </span>
          {dot}
          <span className="font-mono">{job.host}</span>
          {pr !== undefined && (
            <>
              {dot}
              <span className="inline-flex items-center gap-0.5">
                <GitPullRequest className="size-3" aria-hidden="true" />#{pr}
              </span>
            </>
          )}
          {cost > 0 && (
            <>
              {dot}
              <Money usd={cost} />
            </>
          )}
          {runs > 1 && (
            <>
              {dot}
              <span>{runs} runs</span>
            </>
          )}
        </>
      ) : (
        card.labels.slice(0, 3).map((label) => (
          <span key={label} className="rounded-full border px-1.5 text-[11px] leading-[18px]">
            {label}
          </span>
        ))
      )}
    </p>
  );
}

const quiet =
  "relative z-10 inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg border bg-background px-3 text-sm font-medium shadow-xs outline-none transition-colors hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30";

// Action is the one thing a card asks of you, in its stage: assign it,
// answer its agent, look at its pull request, try it again.
function Action({card, hosts, suggested, disabledReason, busy, actions}: {card: Card; hosts: DispatchHost[]; suggested?: Option; disabledReason?: string; busy: boolean; actions: CardActions}) {
  const job = card.job;
  if (!job) {
    return <AssignButton hosts={hosts} repo={card.repo} suggested={suggested} disabledReason={disabledReason} busy={busy} onAssign={(agent, host, picked) => actions.assign(card, agent, host, picked)} />;
  }
  if (card.waiting && job.instance_id) {
    return (
      <button type="button" className={cn(quiet, "border-state-needs/40 text-state-needs hover:bg-state-needs/10")} onClick={() => actions.openChat(job.instance_id)}>
        Answer <ArrowRight className="size-3.5" aria-hidden="true" />
      </button>
    );
  }
  switch (job.state) {
    case "review":
      return job.pr_url ? (
        <a className={quiet} href={job.pr_url} target="_blank" rel="noreferrer">
          Review #{prNumber(job.pr_url)} <ExternalLink className="size-3.5 text-muted-foreground" aria-hidden="true" />
        </a>
      ) : null;
    case "failed":
      return (
        <button type="button" disabled={busy} className={cn(quiet, "disabled:opacity-60")} onClick={() => actions.retry(card)}>
          <RotateCcw className={cn("size-3.5", busy && "animate-spin [animation-direction:reverse]")} aria-hidden="true" /> Retry
        </button>
      );
    case "working":
    case "starting":
      return job.instance_id ? (
        <button
          type="button"
          className={cn(quiet, "w-8 justify-center px-0 text-muted-foreground hover:text-foreground")}
          onClick={() => actions.openChat(job.instance_id)}
          aria-label={`Open ${agentLabel(job.agent)}'s chat`}
          title="Open the chat"
        >
          <MessageSquare className="size-4" aria-hidden="true" />
        </button>
      ) : null;
    default:
      return null;
  }
}

// IssueCard is an issue on the board: its state, number and title, who
// works on it and what it does now, and the one action its stage asks
// for. The title opens its detail; the card stretches the title's target.
export const IssueCard = memo(function IssueCard({
  card,
  hosts,
  instance,
  suggested,
  disabledReason,
  showRepo,
  selected,
  busy,
  flash,
  actions,
}: {
  card: Card;
  hosts: DispatchHost[];
  instance?: HubInstance;
  suggested?: Option;
  disabledReason?: string;
  showRepo: boolean;
  selected: boolean;
  busy: boolean;
  flash: boolean;
  actions: CardActions;
}) {
  const look = lookOf(card);
  const done = look === "done" || look === "stopped";
  return (
    <li
      data-flip={card.key}
      data-selected={selected || undefined}
      style={{"--flash": lookRing[look]} as React.CSSProperties}
      className={cn(
        "group/card relative grid grid-cols-[auto_minmax(0,1fr)] content-start gap-x-3 rounded-2xl border bg-card px-4 py-3.5 shadow-xs transition-[border-color,box-shadow,background-color] duration-200 sm:grid-cols-[auto_minmax(0,1fr)_auto]",
        "hover:border-foreground/15 hover:shadow-md has-[[data-card-title]:focus-visible]:ring-[3px] has-[[data-card-title]:focus-visible]:ring-ring/50",
        selected && "border-foreground/25 bg-accent/50 shadow-md dark:border-foreground/30",
        done && !selected && "bg-card/60 shadow-none",
        flash && "issue-flash",
      )}
    >
      <JobGlyph look={look} className="row-span-2 mt-px" />
      <p className="flex items-baseline gap-2 text-xs text-muted-foreground sm:col-span-2">
        <span className="font-mono tabular-nums">#{card.number}</span>
        {/* To do says itself: the section does, and the Assign button. */}
        {look !== "todo" && <span className={cn("font-medium", lookTone[look])}>{lookLabel[look]}</span>}
        <span className="ml-auto" />
        <Age card={card} />
      </p>
      <div className="min-w-0">
        <button
          type="button"
          data-card-title
          data-issue-key={card.key}
          onClick={() => actions.select(card.key)}
          aria-current={selected || undefined}
          className={cn(
            "mt-1 line-clamp-2 text-left text-[15px] leading-snug font-medium text-pretty outline-none after:absolute after:inset-0 after:rounded-2xl after:content-['']",
            done && "text-foreground/70",
          )}
        >
          {card.title}
        </button>
        <Meta card={card} showRepo={showRepo} />
        <Live card={card} instance={instance} />
      </div>
      <div className="col-start-2 mt-3 flex justify-end empty:hidden sm:col-start-3 sm:row-start-2 sm:mt-0 sm:self-center sm:pl-3">
        <Action card={card} hosts={hosts} suggested={suggested} disabledReason={disabledReason} busy={busy} actions={actions} />
      </div>
    </li>
  );
});
