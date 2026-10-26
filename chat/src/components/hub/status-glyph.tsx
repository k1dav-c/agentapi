import type {InstanceState} from "@/lib/hub";
import {cn} from "@/lib/utils";

// StatusGlyph draws an instance's state with a shape of its own, so the
// state reads without its color: an exclamation, a turning arc, a check, a
// hollow ring, a broken ring, a stop square.
export function StatusGlyph({state, className}: {state: InstanceState; className?: string}) {
  const box = cn("size-5 shrink-0", className);
  switch (state) {
    case "needs-you":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={cn(box, "overflow-visible")}>
          <circle cx="10" cy="10" r="9.5" className="hub-halo fill-state-needs/25" />
          <circle cx="10" cy="10" r="7.5" className="fill-state-needs" />
          <path d="M10 5.6v5.2" className="stroke-background" strokeWidth="2" strokeLinecap="round" />
          <circle cx="10" cy="13.9" r="1.15" className="fill-background" />
        </svg>
      );
    case "working":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="7" className="fill-none stroke-state-working/20" strokeWidth="2.4" />
          <path d="M10 3a7 7 0 0 1 7 7" className="hub-spin fill-none stroke-state-working" strokeWidth="2.4" strokeLinecap="round" style={{transformOrigin: "10px 10px"}} />
        </svg>
      );
    case "unread":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="7.5" className="fill-state-ready" />
          <path d="M6.6 10.3l2.3 2.3 4.6-4.9" className="fill-none stroke-background" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      );
    case "idle":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="6.5" className="fill-none stroke-muted-foreground/45" strokeWidth="1.8" />
          <circle cx="10" cy="10" r="2" className="fill-muted-foreground/45" />
        </svg>
      );
    case "connecting":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="6.5" className="hub-spin-slow fill-none stroke-muted-foreground/60" strokeWidth="1.8" strokeDasharray="3 3.4" style={{transformOrigin: "10px 10px"}} />
        </svg>
      );
    case "unreachable":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <circle cx="10" cy="10" r="6.5" className="fill-none stroke-state-fault" strokeWidth="1.8" strokeDasharray="30 11" transform="rotate(-50 10 10)" />
          <path d="M5.8 14.2l8.4-8.4" className="stroke-state-fault" strokeWidth="1.8" strokeLinecap="round" />
        </svg>
      );
    case "stopped":
      return (
        <svg viewBox="0 0 20 20" aria-hidden="true" className={box}>
          <rect x="4" y="4" width="12" height="12" rx="3.5" className="fill-none stroke-muted-foreground/55" strokeWidth="1.8" />
          <rect x="7.5" y="7.5" width="5" height="5" rx="1" className="fill-muted-foreground/55" />
        </svg>
      );
  }
}

// The text color of each state, for labels next to the glyph.
export const stateTone: Record<InstanceState, string> = {
  "needs-you": "text-state-needs",
  working: "text-state-working",
  unread: "text-state-ready",
  idle: "text-muted-foreground",
  connecting: "text-muted-foreground",
  unreachable: "text-state-fault",
  stopped: "text-muted-foreground",
};
