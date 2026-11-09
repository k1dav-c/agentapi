"use client";

import {useEffect, useRef, useState} from "react";
import {ArrowLeft, ArrowRight, Check, ChevronDown, Copy, ExternalLink, Github, MessageSquare, RotateCcw, SendHorizontal, Square, X} from "lucide-react";
import TextareaAutosize from "react-textarea-autosize";
import {AgentMark} from "@/components/agent-mark";
import {Button} from "@/components/ui/button";
import {DropdownMenu, DropdownMenuContent, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import type {HubInstance} from "@/lib/hub";
import {active, agentLabel, prNumber, type DispatchHost, type DispatchJob} from "@/lib/hub-dispatch";
import {activity, formatElapsed} from "@/lib/hub-format";
import {formatDuration, jobTotals, logPath, switchAgents, type Card, type Option} from "@/lib/hub-issues";
import {formatTokens} from "@/lib/hub-spend";
import {useCopy} from "@/lib/use-copy";
import {cn} from "@/lib/utils";
import {useHubTime} from "../clock";
import {AssignButton, SwitchMenuItems} from "./assign-menu";
import {failure} from "./issue-card";
import {JobGlyph, lookLabel, lookOf, lookTone, type Look} from "./job-glyph";
import {Money} from "./money";
import {RunTimeline} from "./run-timeline";
import type {DispatchActions} from "./use-dispatch";

export interface DetailActions extends DispatchActions {
  openChat: (instanceID: string) => void;
  assignCard: (card: Card, agent: string, host: string, picked: boolean) => void;
}

const heading = "text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase";

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

function CopyLine({label, value, className}: {label: string; value: string; className?: string}) {
  const {copied, copy} = useCopy();
  return (
    <div className={cn("flex min-w-0 items-center gap-3 py-2", className)}>
      <dt className="w-20 shrink-0 text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 truncate font-mono text-[12.5px]" title={value}>
        {value}
      </dd>
      <button
        type="button"
        onClick={() => void copy(value)}
        aria-label={copied ? "Copied" : `Copy the ${label.toLowerCase()}`}
        className="flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        {copied ? <Check className="size-3.5 text-state-ready" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

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

// Status is the block at the top of the detail: the issue's state in a
// word and a sentence, what its agent does or asks now, and what you can
// do about it, the likely thing first.
function Status({
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
  const log = logPath(job?.error);
  const others = job ? switchAgents(hosts, job) : [];
  const live = job && instance && (look === "working" || look === "waiting") ? activity(instance) : undefined;
  // A confirmation is for the job as it was: a change from elsewhere ends it.
  const state = job?.state;
  useEffect(() => setConfirming(null), [state]);

  const confirm = confirming && job && (
    <div role="alertdialog" aria-label="Confirm" className="hub-rise mt-4 rounded-xl border bg-background p-3.5 shadow-sm">
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
        <Button size="sm" variant="ghost" onClick={() => setConfirming(null)} autoFocus>
          Cancel
        </Button>
        <Button
          size="sm"
          variant={confirming.kind === "stop" ? "destructive" : "default"}
          disabled={busy}
          onClick={() => {
            if (confirming.kind === "stop") void actions.stop(job);
            else void actions.switchTo(job, confirming.agent);
            setConfirming(null);
          }}
        >
          {confirming.kind === "stop" ? "Stop and delete the branch" : `Switch to ${agentLabel(confirming.agent)}`}
        </Button>
      </div>
    </div>
  );

  const switchMenu = others.length > 0 && job && (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" variant="ghost" disabled={busy} className="gap-1 text-muted-foreground hover:text-foreground">
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
    <Button size="sm" variant="ghost" disabled={busy} className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive" onClick={() => setConfirming({kind: "stop"})}>
      <Square className="size-3.5" /> Stop
    </Button>
  );
  const chat = job?.instance_id && active(job) && look !== "failed" && (
    <Button size="sm" variant={look === "working" || look === "starting" ? "default" : "outline"} onClick={() => actions.openChat(job.instance_id)}>
      <MessageSquare className="size-3.5" /> Open chat
    </Button>
  );

  return (
    <section aria-label="Status" className={cn("rounded-2xl border p-4", tint[look])}>
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
          <p className="px-3 pt-2.5 pb-0.5 text-[13px] text-state-fault [overflow-wrap:anywhere]">{failure(job.error)}</p>
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

// Totals are what the job's runs add up to.
function Totals({job}: {job: DispatchJob}) {
  const now = useHubTime((n) => Math.floor(n / 15_000) * 15_000);
  const totals = jobTotals(job, now);
  const stats = [
    {label: "Spend", value: <Money usd={totals.usd} />, title: "API-equivalent spend of all its runs"},
    {label: totals.runs === 1 ? "Run" : "Runs", value: String(totals.runs)},
    {label: "Agent time", value: formatDuration(totals.agentSeconds), title: "How long its agents worked on tasks"},
    {label: "Tokens", value: formatTokens(totals.tokens)},
  ];
  return (
    <dl className="grid grid-cols-4 divide-x rounded-2xl border bg-card shadow-xs">
      {stats.map((stat) => (
        <div key={stat.label} className="min-w-0 px-3 py-2.5" title={stat.title}>
          <dt className="truncate text-[11px] text-muted-foreground">{stat.label}</dt>
          <dd className="mt-0.5 truncate text-[17px] font-semibold tracking-tight tabular-nums">{stat.value}</dd>
        </div>
      ))}
    </dl>
  );
}

// Composer sends the job's agent a message, as a review comment would.
function Composer({job, busy, onSend}: {job: DispatchJob; busy: boolean; onSend: (message: string) => Promise<boolean>}) {
  const [text, setText] = useState("");
  const submit = async () => {
    const message = text.trim();
    if (!message || busy) return;
    if (await onSend(message)) setText("");
  };
  return (
    <form
      className="flex items-end gap-2 rounded-2xl border bg-card p-1.5 pl-3.5 shadow-sm transition-[border-color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/30"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <AgentMark agentType={job.agent} className="mb-2.5 size-3.5" />
      <TextareaAutosize
        minRows={1}
        maxRows={6}
        value={text}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            void submit();
          }
        }}
        placeholder={`Message ${agentLabel(job.agent)}…`}
        aria-label={`Message to ${agentLabel(job.agent)}`}
        className="min-w-0 flex-1 resize-none bg-transparent py-2 text-sm outline-none placeholder:text-muted-foreground/80"
      />
      <Button type="submit" size="icon" className="size-9 shrink-0 rounded-xl" disabled={busy || !text.trim()} aria-label="Send" title="Send (⌘↵)">
        <SendHorizontal className="size-4" />
      </Button>
    </form>
  );
}

// Body is the issue's description, folded past a few lines.
function Body({text}: {text: string}) {
  const [open, setOpen] = useState(false);
  const long = text.length > 420 || text.split("\n").length > 8;
  return (
    <div>
      <p className={cn("text-sm leading-relaxed whitespace-pre-wrap text-foreground/85 [overflow-wrap:anywhere]", long && !open && "line-clamp-8")}>
        {/* Code spans read as code; the rest as it was written. */}
        {text.split(/(`[^`\n]+`)/).map((part, i) =>
          part.length > 2 && part.startsWith("`") && part.endsWith("`") ? (
            <code key={i} className="rounded bg-muted px-1 py-px font-mono text-[0.88em]">
              {part.slice(1, -1)}
            </code>
          ) : (
            part
          ),
        )}
      </p>
      {long && (
        <button type="button" onClick={() => setOpen(!open)} className="mt-1 text-xs font-medium text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
          {open ? "Less" : "More"}
        </button>
      )}
    </div>
  );
}

// IssueDetail is one issue whole: what it is, where it is and what to do
// about it, what its runs cost, its timeline, where its work lives on the
// machine, and a line to its agent. As a panel beside the board, or a
// sheet over it on a narrow screen (back instead of close).
export function IssueDetail({
  card,
  hosts,
  instance,
  busy,
  suggested,
  disabledReason,
  actions,
  onClose,
  sheet,
}: {
  card: Card;
  hosts: DispatchHost[];
  instance?: HubInstance;
  busy: boolean;
  suggested?: Option;
  disabledReason?: string;
  actions: DetailActions;
  onClose: () => void;
  sheet: boolean;
}) {
  const job = card.job;
  const look = lookOf(card);
  const title = useRef<HTMLHeadingElement>(null);
  // A new issue's detail reads from its title.
  useEffect(() => title.current?.focus({preventScroll: true}), [card.key]);
  const where = job ? hosts.find((h) => h.hostname === job.host)?.jobs?.find((info) => info.key === job.key) : undefined;
  const talking = job && active(job) && job.state !== "failed" && job.state !== "starting";
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 px-5 pt-[max(0.875rem,env(safe-area-inset-top))] pb-2">
        <Button variant="ghost" size="icon" className="-ml-2 size-9 rounded-full text-muted-foreground" onClick={onClose} aria-label={sheet ? "Back to the board" : "Close the issue"}>
          {sheet ? <ArrowLeft className="size-4" /> : <X className="size-4" />}
        </Button>
        <p className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">
          {card.repo} <span className="text-foreground">#{card.number}</span>
        </p>
        <Button variant="ghost" size="sm" asChild className="text-muted-foreground">
          <a href={card.url} target="_blank" rel="noreferrer" aria-label="Open the issue on GitHub">
            <Github className="size-4" /> <span className="max-sm:sr-only">GitHub</span>
          </a>
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 pb-6">
        <h2 ref={title} tabIndex={-1} className="text-[22px] leading-tight font-semibold tracking-tight text-balance outline-none">
          {card.title}
        </h2>
        {card.labels.length > 0 && (
          <p className="mt-2.5 flex flex-wrap gap-1.5">
            {card.labels.map((label) => (
              <span key={label} className="rounded-full border px-2 text-[11px] leading-5 text-muted-foreground">
                {label}
              </span>
            ))}
          </p>
        )}
        <div className="mt-5">
          <Status card={card} job={job} look={look} hosts={hosts} instance={instance} suggested={suggested} disabledReason={disabledReason} busy={busy} actions={actions} />
        </div>
        {job && (
          <div className="mt-4">
            <Totals job={job} />
          </div>
        )}
        {card.body && (
          <section className="mt-7">
            <h3 className={heading}>Description</h3>
            <div className="mt-2.5">
              <Body text={card.body} />
            </div>
          </section>
        )}
        {job && (
          <section className="mt-7">
            <h3 className={heading}>Timeline</h3>
            <div className="mt-3.5">
              <RunTimeline job={job} onOpenChat={actions.openChat} />
            </div>
          </section>
        )}
        {where && (
          <section className="mt-7">
            <h3 className={heading}>On the machine</h3>
            <dl className="mt-1.5 divide-y">
              <CopyLine label="Branch" value={where.branch} />
              <CopyLine label="Worktree" value={where.worktree} />
            </dl>
          </section>
        )}
      </div>
      {talking && (
        <div className="border-t bg-background/85 px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur">
          <Composer job={job} busy={busy} onSend={(message) => actions.send(job, message)} />
        </div>
      )}
    </div>
  );
}
