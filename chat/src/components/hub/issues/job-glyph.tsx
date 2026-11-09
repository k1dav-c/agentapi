import type {DispatchJob} from "@/lib/hub-dispatch";
import type {Card} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";
import {StatusGlyph} from "../status-glyph";

// What a card's state is called, and its tone, by what the glyph shows.
export type Look = "todo" | "starting" | "working" | "waiting" | "review" | "failed" | "done" | "stopped";

export function lookOf(card: Pick<Card, "job" | "waiting">): Look {
  const state: DispatchJob["state"] | undefined = card.job?.state;
  if (!state) return "todo";
  if (card.waiting) return "waiting";
  return state === "starting" ? "starting" : state === "working" ? "working" : state;
}

export const lookLabel: Record<Look, string> = {
  todo: "To do",
  starting: "Starting",
  working: "Working",
  waiting: "Waits for you",
  review: "In review",
  failed: "Failed",
  done: "Done",
  stopped: "Stopped",
};

// The text color of each look; the state colors mean what they mean in
// the rest of the hub: needs (amber) wants you, working (blue) runs,
// ready (green) has something to look at, fault (red) broke.
export const lookTone: Record<Look, string> = {
  todo: "text-muted-foreground",
  starting: "text-state-working",
  working: "text-state-working",
  waiting: "text-state-needs",
  review: "text-state-ready",
  failed: "text-state-fault",
  done: "text-muted-foreground",
  stopped: "text-muted-foreground",
};

// The ring a card flashes in when it changes to the look.
export const lookRing: Record<Look, string> = {
  todo: "var(--muted-foreground)",
  starting: "var(--state-working)",
  working: "var(--state-working)",
  waiting: "var(--state-needs)",
  review: "var(--state-ready)",
  failed: "var(--state-fault)",
  done: "var(--muted-foreground)",
  stopped: "var(--muted-foreground)",
};

// JobGlyph draws a card's look with a shape of its own, as StatusGlyph
// does an agent's: a dashed ring to do, the turning arc, the halo of what
// waits for you, a branch merging for review, a cross for a failure, a
// check done and a square stopped.
export function JobGlyph({look, className}: {look: Look; className?: string}) {
  const box = cn("size-5 shrink-0", className);
  switch (look) {
    case "todo":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="6.5" className="fill-none stroke-muted-foreground/55" strokeWidth="1.6" strokeDasharray="2.6 2.5" />
        </svg>
      );
    case "starting":
      return <StatusGlyph state="connecting" className={cn(box, "[&_circle]:stroke-state-working")} />;
    case "working":
      return <StatusGlyph state="working" className={box} />;
    case "waiting":
      return <StatusGlyph state="needs-you" className={box} />;
    case "review":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="8" className="fill-state-ready/14" />
          <circle cx="7" cy="6.2" r="1.6" className="fill-none stroke-state-ready" strokeWidth="1.5" />
          <circle cx="7" cy="13.8" r="1.6" className="fill-none stroke-state-ready" strokeWidth="1.5" />
          <circle cx="13" cy="13.8" r="1.6" className="fill-none stroke-state-ready" strokeWidth="1.5" />
          <path d="M7 7.8v4.4M13 12.2V9.6a2 2 0 0 0-2-2H9.6" className="fill-none stroke-state-ready" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      );
    case "failed":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="7.5" className="fill-state-fault" />
          <path d="M7.4 7.4l5.2 5.2M12.6 7.4l-5.2 5.2" className="stroke-background" strokeWidth="1.9" strokeLinecap="round" />
        </svg>
      );
    case "done":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="7.5" className="fill-muted-foreground/22" />
          <path d="M6.7 10.3l2.2 2.2 4.4-4.7" className="fill-none stroke-muted-foreground" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      );
    case "stopped":
      return <StatusGlyph state="stopped" className={box} />;
  }
}
