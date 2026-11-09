"use client";

import {useEffect, useRef} from "react";
import {ArrowLeft, Github, X} from "lucide-react";
import {Button} from "@/components/ui/button";
import type {HubInstance} from "@/lib/hub";
import {active, type DispatchHost} from "@/lib/hub-dispatch";
import type {Card, Option} from "@/lib/hub-issues";
import {sectionClass} from "../styles";
import {Body, Composer, CopyLine, Totals} from "./detail-parts";
import {Status} from "./issue-status";
import {lookOf} from "./job-glyph";
import {RunTimeline} from "./run-timeline";
import type {DispatchActions} from "./use-dispatch";

export interface DetailActions extends DispatchActions {
  openChat: (instanceID: string) => void;
  assignCard: (card: Card, agent: string, host: string, picked: boolean) => void;
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
    <div className="flex h-full min-h-0 flex-col" data-issue-detail>
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
            <h3 className={sectionClass}>Description</h3>
            <div className="mt-2.5">
              <Body text={card.body} />
            </div>
          </section>
        )}
        {job && (
          <section className="mt-7">
            <h3 className={sectionClass}>Timeline</h3>
            <div className="mt-3.5">
              <RunTimeline job={job} onOpenChat={actions.openChat} />
            </div>
          </section>
        )}
        {where && (
          <section className="mt-7">
            <h3 className={sectionClass}>On the machine</h3>
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
