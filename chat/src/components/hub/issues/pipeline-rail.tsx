"use client";

import {pipeline, type SectionID, type Stage} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";

const tone: Record<Stage, {text: string; bar: string}> = {
  todo: {text: "text-foreground", bar: "bg-muted-foreground/35"},
  working: {text: "text-state-working", bar: "bg-state-working"},
  failed: {text: "text-state-fault", bar: "bg-state-fault"},
  review: {text: "text-state-ready", bar: "bg-state-ready"},
  done: {text: "text-muted-foreground", bar: "bg-muted-foreground/20"},
};

// The section a stage's cards are in.
const sectionFor: Record<Exclude<Stage, "failed">, SectionID> = {todo: "todo", working: "working", review: "attention", done: "done"};

// The order the track draws the stages in: a failure sits where the work
// stopped, between working and review.
const trackOrder: Stage[] = ["todo", "working", "failed", "review", "done"];

// PipelineRail is the board at a glance: how many issues are at each
// stage, left to right as work flows, over one track that splits in their
// proportions; in progress says how many of its own wait on you or failed,
// which the board shows under Needs you. A stage jumps to its section.
export function PipelineRail({counts, onJump}: {counts: Record<Stage | "waiting", number>; onJump: (section: SectionID) => void}) {
  const total = trackOrder.reduce((sum, stage) => sum + counts[stage], 0);
  return (
    <nav aria-label="Stages" className="rounded-2xl border bg-card px-1.5 pt-1.5 pb-3 shadow-xs">
      <ol className="grid grid-cols-[0.8fr_1.35fr_1.1fr_0.75fr] sm:grid-cols-4">
        {pipeline.map(({stage, label}, i) => {
          // In progress counts what failed in it: the waiting and the failed
          // are said beside it.
          const count = stage === "working" ? counts.working + counts.failed : counts[stage];
          return (
            <li key={stage} className="relative min-w-0">
              {i > 0 && <span aria-hidden="true" className="absolute top-1/2 left-0 h-6 w-px -translate-y-1/2 bg-border" />}
              <button
                type="button"
                onClick={() => onJump(sectionFor[stage])}
                className="w-full rounded-xl px-2 py-2 text-left outline-none transition-colors hover:bg-accent/70 focus-visible:ring-[3px] focus-visible:ring-ring/50 sm:px-3.5"
                aria-label={`${count} ${label}${stage === "working" && counts.waiting ? `, ${counts.waiting} waiting on you` : ""}${stage === "working" && counts.failed ? `, ${counts.failed} failed` : ""}: go to them`}
              >
                <span className="block text-[10.5px] font-medium tracking-[0.02em] whitespace-nowrap text-muted-foreground uppercase sm:text-[11px] sm:tracking-[0.06em]">{label}</span>
                <span className="mt-0.5 flex items-baseline gap-1.5">
                  {/* A changed count rises into place. */}
                  <span key={count} className={cn("hub-rise text-2xl leading-none font-semibold tracking-tight tabular-nums sm:text-[28px]", count > 0 ? tone[stage].text : "text-muted-foreground/40")}>
                    {count}
                  </span>
                  {stage === "working" && (counts.waiting > 0 || counts.failed > 0) && (
                    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs font-medium whitespace-nowrap tabular-nums">
                      {counts.waiting > 0 && (
                        <span className="inline-flex items-center gap-1 text-state-needs">
                          <span className="size-1.5 rounded-full bg-state-needs" aria-hidden="true" />
                          {counts.waiting}
                          <span className="max-sm:hidden">{counts.waiting === 1 ? "waits" : "wait"}</span>
                        </span>
                      )}
                      {counts.failed > 0 && (
                        <span className="inline-flex items-center gap-1 text-state-fault">
                          <span className="size-1.5 rounded-full bg-state-fault" aria-hidden="true" />
                          {counts.failed}
                          <span className="max-sm:hidden">failed</span>
                        </span>
                      )}
                    </span>
                  )}
                </span>
              </button>
            </li>
          );
        })}
      </ol>
      <div aria-hidden="true" className="mx-2.5 mt-1.5 flex h-1.5 gap-[3px] overflow-hidden rounded-full sm:mx-3.5">
        {total === 0 ? (
          <span className="flex-1 rounded-full bg-muted" />
        ) : (
          trackOrder
            .filter((stage) => counts[stage] > 0)
            .map((stage) => (
              <span
                key={stage}
                className={cn("rounded-full transition-[flex-grow] duration-500 ease-[cubic-bezier(0.2,0.8,0.2,1)]", tone[stage].bar, stage === "working" && "hub-shimmer")}
                style={{flexGrow: counts[stage], flexBasis: 0}}
              />
            ))
        )}
      </div>
    </nav>
  );
}
