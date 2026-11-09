"use client";

import {CircleDot, Github, MessageSquareText} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {agentLabel, type DispatchJob} from "@/lib/hub-dispatch";
import {endReason, formatDuration, messageFrom, runSeconds, timeline} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";
import {useHubTime} from "../clock";
import {Money} from "./money";

// clock is a time of day, with the day when it isn't today.
export function clock(at: string | undefined, now: number): string {
  if (!at) return "";
  const date = new Date(at);
  if (Number.isNaN(date.getTime())) return "";
  const time = date.toLocaleTimeString(undefined, {hour: "2-digit", minute: "2-digit"});
  return new Date(now).toDateString() === date.toDateString() ? time : `${date.toLocaleDateString(undefined, {month: "short", day: "numeric"})} ${time}`;
}

// RunTimeline is a job's story, the latest first: each run (the agent and
// its machine, when it ran and for how long, what it cost and how it
// ended) and each message its agents were sent, from the hub or a review
// or comment on GitHub, down to when it was assigned.
export function RunTimeline({job, onOpenChat}: {job: DispatchJob; onOpenChat: (instanceID: string) => void}) {
  const now = useHubTime((n) => Math.floor(n / 15_000) * 15_000);
  const entries = timeline(job);
  return (
    <ol className="relative">
      {entries.map((entry) => {
        if (entry.kind === "message") {
          const {message} = entry;
          const hub = message.source === "hub";
          return (
            <li key={`message ${entry.at}`} className="relative flex gap-3 pb-5">
              <Rail />
              <span className="relative z-10 flex size-7 shrink-0 items-center justify-center rounded-full border bg-background">
                {hub ? <MessageSquareText className="size-3.5 text-muted-foreground" aria-hidden="true" /> : <Github className="size-3.5 text-muted-foreground" aria-hidden="true" />}
              </span>
              <div className="min-w-0 flex-1 pt-0.5">
                <p className="flex items-baseline gap-2 text-xs text-muted-foreground">
                  <span className="min-w-0">
                    <span className="font-medium text-foreground">{messageFrom(message)}</span> to {agentLabel(message.agent)}
                  </span>
                  <span className="ml-auto shrink-0 tabular-nums">{clock(entry.at, now)}</span>
                </p>
                {message.text && (
                  <p className={cn("mt-1.5 rounded-xl rounded-tl-sm px-3 py-2 text-sm whitespace-pre-wrap [overflow-wrap:anywhere]", hub ? "bg-prompt" : "border bg-card")}>{message.text}</p>
                )}
              </div>
            </li>
          );
        }
        const {run, index, next, current} = entry;
        const seconds = runSeconds(run, now);
        const ended = endReason(run, next);
        return (
          <li key={`run ${index}`} className="relative flex gap-3 pb-5">
            <Rail />
            <span className={cn("relative z-10 flex size-7 shrink-0 items-center justify-center rounded-full border bg-background", current && "border-state-working/50")}>
              {current && <span className="absolute inset-0 animate-ping rounded-full bg-state-working/20 [animation-duration:2.4s]" aria-hidden="true" />}
              <AgentMark agentType={run.agent} className="size-3.5" />
            </span>
            <div className="min-w-0 flex-1 pt-0.5">
              <p className="flex items-baseline gap-2 text-sm">
                <span className="font-medium">{agentLabel(run.agent)}</span>
                <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">{run.host}</span>
                <span className="ml-auto shrink-0 text-sm tabular-nums">{(run.cost?.usd ?? 0) > 0 ? <Money usd={run.cost.usd} /> : <span className="text-muted-foreground/60">—</span>}</span>
              </p>
              <p className="mt-0.5 flex flex-wrap items-baseline gap-x-1.5 text-xs text-muted-foreground tabular-nums">
                <span>
                  Run {index + 1}
                  {run.round > 1 && ` · round ${run.round}`}
                </span>
                <span aria-hidden="true">·</span>
                <span>
                  {clock(run.started_at, now)}
                  {run.ended_at && ` – ${clock(run.ended_at, now)}`}
                </span>
                {seconds > 0 && (
                  <>
                    <span aria-hidden="true">·</span>
                    <span className={cn(current && "text-state-working")}>{formatDuration(seconds)}</span>
                  </>
                )}
              </p>
              {current ? (
                <button type="button" onClick={() => onOpenChat(run.instance_id)} className="mt-1 text-xs font-medium text-state-working underline-offset-4 hover:underline">
                  {job.state === "starting" ? "Starting now" : job.state === "working" ? "Running now" : "Current run"} · open the chat
                </button>
              ) : (
                ended && <p className={cn("mt-1 text-xs", run.end_reason === "failed" ? "text-state-fault" : "text-muted-foreground")}>{ended}</p>
              )}
            </div>
          </li>
        );
      })}
      <li className="relative flex gap-3">
        <span className="relative z-10 flex size-7 shrink-0 items-center justify-center">
          <CircleDot className="size-3.5 text-muted-foreground/70" aria-hidden="true" />
        </span>
        <p className="flex-1 pt-1 text-xs text-muted-foreground">
          Assigned <span className="tabular-nums">{clock(job.created_at, now)}</span>
        </p>
      </li>
    </ol>
  );
}

// Rail is the line that joins a timeline's entries.
function Rail() {
  return <span aria-hidden="true" className="absolute top-7 bottom-0 left-[13.5px] w-px bg-border" />;
}
