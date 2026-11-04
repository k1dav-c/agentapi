"use client";

import type {ReactNode} from "react";
import {AgentMark} from "@/components/agent-mark";
import {agentLabel, toggleAgent, type HubAgent, type HubFilter} from "@/lib/hub";
import {cn} from "@/lib/utils";

const filters: {id: HubFilter; label: string}[] = [
  {id: "all", label: "All"},
  {id: "needs-you", label: "Need you"},
  {id: "working", label: "Working"},
  {id: "unread", label: "Unseen"},
  {id: "offline", label: "Offline"},
];

// A row of chips that scrolls sideways when the list is narrower than it.
function ChipRow({label, children}: {label: string; children: ReactNode}) {
  return (
    <div
      role="group"
      aria-label={label}
      className="-mx-4 flex gap-1.5 overflow-x-auto px-4 pb-0.5 [mask-image:linear-gradient(to_right,black_calc(100%-28px),transparent)] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {children}
    </div>
  );
}

function Chip({active, count, attention, onClick, children}: {active: boolean; count: number; attention?: boolean; onClick: () => void; children: ReactNode}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-3 text-xs font-medium outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
        active ? "border-foreground bg-foreground text-background" : "bg-background text-muted-foreground hover:text-foreground",
        !active && attention && "border-state-needs/40 text-state-needs",
      )}
    >
      {children}
      <span className={cn("tabular-nums", active ? "text-background/70" : "text-muted-foreground/70")}>{count}</span>
    </button>
  );
}

// StatusChips filter the list by state; picking the chip on again shows all.
// counts are of the agents picked.
export function StatusChips({filter, counts, onChange}: {filter: HubFilter; counts: Record<HubFilter, number>; onChange: (filter: HubFilter) => void}) {
  return (
    <ChipRow label="Filter the agents">
      {filters.map((f) => {
        const active = filter === f.id;
        return (
          <Chip
            key={f.id}
            active={active}
            count={counts[f.id]}
            attention={f.id === "needs-you" && counts[f.id] > 0}
            onClick={() => onChange(active && f.id !== "all" ? "all" : f.id)}
          >
            {f.label}
          </Chip>
        );
      })}
    </ChipRow>
  );
}

// AgentChips filter the list by agent, any number at once: Any agent picks
// none, which is all of them; picking a chip again takes it out. agents are
// the ones the instances run; counts are of the state filtered.
export function AgentChips({
  agents,
  picked,
  counts,
  onChange,
}: {
  agents: readonly HubAgent[];
  picked: ReadonlySet<HubAgent>;
  counts: Record<HubAgent, number>;
  onChange: (picked: Set<HubAgent>) => void;
}) {
  const any = agents.reduce((sum, agent) => sum + counts[agent], 0);
  return (
    <ChipRow label="Filter by agent">
      <Chip active={picked.size === 0} count={any} onClick={() => onChange(new Set())}>
        Any agent
      </Chip>
      {agents.map((agent) => {
        const active = picked.has(agent);
        return (
          <Chip
            key={agent}
            active={active}
            count={counts[agent]}
            onClick={() => onChange(toggleAgent(picked, agent))}
          >
            {/* On a disc of the page's color, so the mark shows on a picked chip too. */}
            <span className="-ml-1 flex size-5 items-center justify-center rounded-full bg-background">
              <AgentMark agentType={agent === "other" ? "" : agent} className="size-3" />
            </span>
            {agentLabel(agent)}
          </Chip>
        );
      })}
    </ChipRow>
  );
}
