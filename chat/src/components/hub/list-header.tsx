"use client";

import {Bell, BellOff, Plus, Search, Settings, X} from "lucide-react";
import {ModeToggle} from "@/components/mode-toggle";
import {Button} from "@/components/ui/button";
import type {ConnectionStatus} from "@/lib/event-source";
import {formatUSD, spendTitle, type SpendTotals} from "@/lib/hub-spend";
import {cn} from "@/lib/utils";
import {HubMark} from "./brand";
import {AgentChips, StatusChips} from "./filter-chips";
import {ByMachineToggle, ListOrderSwitch, type HubListView} from "./list-order";
import type {ListFilters} from "./use-list-filters";

export interface HubListHeaderProps {
  // status is the hub's stream's; unreachable, that the hub didn't answer
  // the last session check after it failed.
  status: ConnectionStatus;
  unreachable: boolean;
  spend: SpendTotals;
  notify: boolean;
  onToggleNotify: () => void;
  onSettings: () => void;
  onAdd: () => void;
  search: React.RefObject<HTMLInputElement | null>;
  filters: ListFilters;
  view: HubListView;
}

// HubListHeader heads the hub's list: the hub's name and whether it is
// live, what the agents spent today, notifications, the theme, the
// settings and Add; the search, the list's order and By machine; and the
// state and agent chips.
export function HubListHeader({status, unreachable, spend, notify, onToggleNotify, onSettings, onAdd, search, filters, view}: HubListHeaderProps) {
  const connected = status === "connected";
  const {counts, query, setQuery} = filters;
  return (
    <header className="space-y-3 border-b px-4 pt-[max(1rem,env(safe-area-inset-top))] pb-3">
      <div className="flex items-center gap-2.5">
        <HubMark className="size-8" />
        <div className="min-w-0 flex-1">
          <h1 className="text-[15px] leading-tight font-semibold tracking-tight">AgentAPI Hub</h1>
          <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
            <span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", connected ? "bg-state-ready" : "animate-pulse bg-state-needs")} />
            <span aria-live="polite" className="shrink-0">
              {connected
                ? `Live · ${counts.all} ${counts.all === 1 ? "agent" : "agents"}`
                : status === "offline"
                  ? "Offline"
                  : unreachable
                    ? "Can't reach the hub · retrying"
                    : "Reconnecting…"}
            </span>
            {/* Out of the live region: it changes every minute. */}
            {connected && spend.week > 0 && (
              <span className="min-w-0 truncate tabular-nums" title={spendTitle(spend.day, spend.week)}>
                · {formatUSD(spend.day)}/24h
              </span>
            )}
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-9 rounded-full text-muted-foreground"
          onClick={onToggleNotify}
          aria-pressed={notify}
          aria-label={notify ? "Stop notifying me" : "Notify me when an agent needs me"}
          title={notify ? "Notifications on" : "Notify me when an agent needs me"}
        >
          {notify ? <Bell className="size-4 text-foreground" /> : <BellOff className="size-4" />}
        </Button>
        <ModeToggle />
        <Button variant="ghost" size="icon" className="size-9 rounded-full text-muted-foreground" aria-label="Hub settings" title="Hub settings" onClick={onSettings}>
          <Settings className="size-4" />
        </Button>
        <Button variant="outline" size="icon" className="size-9 rounded-full" aria-label="Add agents" title="Add agents" onClick={onAdd}>
          <Plus className="size-4" />
        </Button>
      </div>
      <div className="flex gap-2">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
          <input
            ref={search}
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search agents"
            aria-label="Search agents"
            className="h-10 w-full rounded-xl border bg-muted/50 pr-10 pl-9 text-sm outline-none transition-colors placeholder:text-muted-foreground/80 focus-visible:border-ring focus-visible:bg-card focus-visible:ring-[3px] focus-visible:ring-ring/40 [&::-webkit-search-cancel-button]:hidden"
          />
          {query ? (
            <button
              type="button"
              onClick={() => setQuery("")}
              aria-label="Clear the search"
              className="absolute top-1/2 right-1 flex size-8 -translate-y-1/2 items-center justify-center rounded-lg text-muted-foreground hover:text-foreground"
            >
              <X className="size-4" />
            </button>
          ) : (
            <kbd className="pointer-events-none absolute top-1/2 right-2.5 hidden -translate-y-1/2 rounded border bg-background px-1.5 font-mono text-[11px] text-muted-foreground md:block">/</kbd>
          )}
        </div>
        <ListOrderSwitch order={view.order} onChange={view.setOrder} />
        <ByMachineToggle on={view.byMachine} onChange={view.setByMachine} />
      </div>
      <div className="space-y-2">
        <StatusChips filter={filters.filter} counts={filters.statusCounts} onChange={filters.setFilter} />
        {filters.presentAgents.length > 1 && <AgentChips agents={filters.presentAgents} picked={filters.agents} counts={filters.agentCounts} onChange={filters.pickAgents} />}
      </div>
    </header>
  );
}
