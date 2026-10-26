import {cn} from "@/lib/utils";
import {StatusGlyph} from "./status-glyph";

// HubMark is the hub's mark: one center the agents around it report to.
export function HubMark({className}: {className?: string}) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={cn("shrink-0", className)}>
      <rect width="32" height="32" rx="9" className="fill-foreground" />
      <circle cx="16" cy="16" r="9.5" fill="none" className="stroke-background/25" strokeWidth="1.2" />
      <path d="M16 16 L9.3 9.3 M16 16 L24.6 11.2 M16 16 L13.4 25" className="stroke-background/45" strokeWidth="1.4" strokeLinecap="round" />
      <circle cx="9.3" cy="9.3" r="2.2" className="fill-state-needs" />
      <circle cx="24.6" cy="11.2" r="2.2" className="fill-state-working" />
      <circle cx="13.4" cy="25" r="2.2" className="fill-state-ready" />
      <circle cx="16" cy="16" r="3.4" className="fill-background" />
    </svg>
  );
}

// Constellation is the sign-in page's picture of the hub: agents on two
// orbits, each in a state, reporting to the center.
export function Constellation({className}: {className?: string}) {
  const nodes = [
    {x: 120, y: 60, r: 9, tone: "needs", label: "needs you"},
    {x: 300, y: 92, r: 8, tone: "working"},
    {x: 330, y: 250, r: 7, tone: "ready"},
    {x: 92, y: 262, r: 7, tone: "idle"},
    {x: 48, y: 160, r: 6, tone: "working"},
    {x: 226, y: 330, r: 6, tone: "idle"},
    {x: 360, y: 168, r: 5, tone: "idle"},
  ] as const;
  const fill = {needs: "fill-state-needs", working: "fill-state-working", ready: "fill-state-ready", idle: "fill-muted-foreground/40"} as const;
  return (
    <svg viewBox="0 0 400 400" aria-hidden="true" className={cn("hub-constellation", className)}>
      <circle cx="200" cy="200" r="150" className="fill-none stroke-border" strokeWidth="1" />
      <circle cx="200" cy="200" r="96" className="fill-none stroke-border" strokeWidth="1" strokeDasharray="2 6" />
      {nodes.map((n, i) => (
        <line key={`l${i}`} x1="200" y1="200" x2={n.x} y2={n.y} className="hub-link stroke-muted-foreground/25" strokeWidth="1" style={{animationDelay: `${i * 0.6}s`}} />
      ))}
      {nodes.map((n, i) => (
        <g key={`n${i}`} className="hub-node" style={{animationDelay: `${i * 0.45}s`, transformOrigin: `${n.x}px ${n.y}px`}}>
          {n.tone === "needs" && <circle cx={n.x} cy={n.y} r={n.r + 9} className="hub-halo fill-state-needs/20" />}
          <circle cx={n.x} cy={n.y} r={n.r} className={fill[n.tone]} />
        </g>
      ))}
      <circle cx="200" cy="200" r="34" className="fill-background stroke-border" strokeWidth="1" />
      <circle cx="200" cy="200" r="15" className="fill-foreground" />
      <circle cx="200" cy="200" r="5" className="fill-background" />
    </svg>
  );
}

// ConstellationScene is the sign-in picture: the constellation on a soft
// glow, with three agents named the way the hub shows them.
export function ConstellationScene({className}: {className?: string}) {
  const chips = [
    {style: {left: "4%", top: "4%"}, state: "needs-you", name: "checkout-api", detail: "Approval · Deploy the release", tone: "text-state-needs", delay: "0s"},
    {style: {right: "0%", top: "30%"}, state: "working", name: "billing-service", detail: "Working · 2m 14s", tone: "text-state-working", delay: "1.2s"},
    {style: {left: "14%", bottom: "6%"}, state: "unread", name: "design-system", detail: "Finished · just now", tone: "text-state-ready", delay: "2.4s"},
  ] as const;
  return (
    <div className={cn("relative", className)}>
      <div aria-hidden="true" className="absolute inset-[12%] rounded-full bg-[radial-gradient(circle,color-mix(in_oklch,var(--state-working)_16%,transparent),transparent_70%)] blur-2xl" />
      <Constellation className="relative w-full" />
      {chips.map((chip) => (
        <div
          key={chip.name}
          className="hub-node absolute flex items-center gap-2 rounded-xl border bg-card/90 py-2 pr-3 pl-2.5 shadow-lg shadow-black/5 backdrop-blur"
          style={{...chip.style, animationDelay: chip.delay}}
        >
          <StatusGlyph state={chip.state} className="size-4" />
          <span className="leading-tight">
            <span className="block text-xs font-semibold">{chip.name}</span>
            <span className={cn("block text-[11px]", chip.tone)}>{chip.detail}</span>
          </span>
        </div>
      ))}
    </div>
  );
}
