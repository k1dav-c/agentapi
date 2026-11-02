"use client";

import {CircleAlert, Hourglass, LoaderCircle} from "lucide-react";
import type {InstanceUpdate} from "@/lib/hub";
import {updateBadge, type UpdateBadge} from "@/lib/hub-update";
import {cn} from "@/lib/utils";

const tones: Record<UpdateBadge["tone"], {icon: typeof LoaderCircle; className: string}> = {
  working: {icon: LoaderCircle, className: "bg-state-working/10 text-state-working"},
  waiting: {icon: Hourglass, className: "bg-muted text-foreground/80"},
  fault: {icon: CircleAlert, className: "bg-state-fault/10 text-state-fault"},
};

// UpdateChip is an AgentAPI update under way, or one that failed, with the
// whole of it (the error of a failed one) in its tooltip; compact says it
// shorter on a phone.
export function UpdateChip({update, compact = false, className}: {update: InstanceUpdate; compact?: boolean; className?: string}) {
  const badge = updateBadge(update);
  const {icon: Icon, className: tone} = tones[badge.tone];
  return (
    <span className={cn("flex min-w-0 items-center gap-1 rounded-full px-1.5 py-px font-medium", tone, className)} title={badge.title} data-update-state={update.state}>
      <Icon aria-hidden="true" className={cn("size-3 shrink-0", Icon === LoaderCircle && "animate-spin")} />
      <span className="sr-only">{badge.title}</span>
      {compact && (
        <span aria-hidden="true" className="truncate sm:hidden">
          {badge.short}
        </span>
      )}
      <span aria-hidden="true" className={cn("truncate", compact && "hidden sm:block")}>
        {badge.label}
      </span>
    </span>
  );
}

// UpdateStatus is the chip in the bar above an open instance, with Cancel
// while the update waits for the agent to be idle.
export function UpdateStatus({update, onCancel}: {update: InstanceUpdate; onCancel: () => void}) {
  return (
    <>
      <span aria-hidden="true" className="text-muted-foreground/40">·</span>
      <UpdateChip update={update} compact />
      {update.state === "waiting" && (
        <button
          type="button"
          onClick={onCancel}
          aria-label="Cancel the update"
          className="-my-1 shrink-0 rounded-sm px-1 py-1 font-medium text-foreground underline underline-offset-2 outline-none hover:no-underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          Cancel
        </button>
      )}
    </>
  );
}
