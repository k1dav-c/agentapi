"use client";

import {Bell, BellOff, CircleDot, Plus, Search, Settings, Volume2, X} from "lucide-react";
import {ModeToggle} from "@/components/mode-toggle";
import {Button} from "@/components/ui/button";
import {DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import type {ConnectionStatus} from "@/lib/event-source";
import {formatUSDShort} from "@/lib/format";
import {spendTitle, type SpendTotals} from "@/lib/hub-spend";
import {parseVersion} from "@/lib/hub-version";
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
  // hubVersion is the AgentAPI version the hub runs; "" until it says.
  hubVersion: string;
  spend: SpendTotals;
  notify: boolean;
  onToggleNotify: () => void;
  // sound is whether an agent that needs you chimes (alert-sound).
  sound: boolean;
  onToggleSound: () => void;
  onSettings: () => void;
  // onHome is the hub's mark: the list and its overview, past an agent open
  // and Issues.
  onHome: () => void;
  onAdd: () => void;
  // onIssues opens the issue dispatch, or closes it when issuesOpen.
  issuesOpen: boolean;
  // issuesNeedYou counts the issues that need you, for the button's badge.
  issuesNeedYou: number;
  onIssues: () => void;
  search: React.RefObject<HTMLInputElement | null>;
  filters: ListFilters;
  view: HubListView;
}

// hubLabel names the hub's version: v3.12.1, or dev for a build of no
// release.
export function hubLabel(version: string): string {
  return parseVersion(version) ? `v${version}` : "dev";
}

// HubListHeader heads the hub's list: the hub's name and whether it is
// live, what the agents spent today, notifications, the theme, the
// settings and Add; the search, the list's order and By machine; and the
// state and agent chips.
export function HubListHeader({status, unreachable, hubVersion, spend, notify, onToggleNotify, sound, onToggleSound, onSettings, onHome, onAdd, issuesOpen, issuesNeedYou, onIssues, search, filters, view}: HubListHeaderProps) {
  const connected = status === "connected";
  const {counts, query, setQuery} = filters;
  return (
    // A container: the list's width, which a person can narrow, decides what
    // its first row has room for.
    <header className="@container space-y-3 border-b px-4 pt-[max(1rem,env(safe-area-inset-top))] pb-3">
      {/* The status line has a row of its own: beside the buttons, the
          name keeps too little room for it in the list's width. */}
      <div className="space-y-0.5">
        {/* Narrower than 20rem, the name gives its room to the buttons; the
            mark stays, and takes you home. */}
        <div className="flex items-center gap-1 @[20rem]:gap-2.5">
          <button
            type="button"
            onClick={onHome}
            aria-label="Overview"
            title="Overview"
            className="shrink-0 rounded-lg outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
          >
            <HubMark className="size-8" />
          </button>
          <h1 className="min-w-0 flex-1 text-[15px] leading-tight font-semibold tracking-tight">
            <span className="sr-only @[20rem]:not-sr-only">AgentAPI Hub</span>
          </h1>
          {/* How the hub tells you an agent needs you while you look away. */}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-9 rounded-full text-muted-foreground"
                aria-label={notify || sound ? "Alerts on" : "Alerts off"}
                title="Alerts when an agent needs you"
              >
                {notify || sound ? <Bell className="size-4 text-foreground" /> : <BellOff className="size-4" />}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-60">
              <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">When an agent needs you</DropdownMenuLabel>
              <DropdownMenuCheckboxItem checked={notify} onCheckedChange={onToggleNotify} onSelect={(event) => event.preventDefault()}>
                <Bell /> Notifications
              </DropdownMenuCheckboxItem>
              <DropdownMenuCheckboxItem checked={sound} onCheckedChange={onToggleSound} onSelect={(event) => event.preventDefault()}>
                <Volume2 /> Sound
              </DropdownMenuCheckboxItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <ModeToggle />
          <Button
            variant="ghost"
            size="icon"
            className={cn("relative size-9 rounded-full text-muted-foreground", issuesOpen && "bg-accent text-foreground")}
            aria-label={issuesNeedYou > 0 ? `Issues, ${issuesNeedYou} ${issuesNeedYou === 1 ? "needs" : "need"} you` : "Issues"}
            aria-pressed={issuesOpen}
            title="Issues: hand GitHub issues to agents (i)"
            onClick={onIssues}
          >
            <CircleDot className="size-4" />
            {/* As a group's header counts what needs you. */}
            {issuesNeedYou > 0 && (
              <span
                aria-hidden="true"
                className="absolute -top-0.5 -right-0.5 inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-state-needs px-1 text-[10px] font-semibold text-background tabular-nums ring-2 ring-background"
              >
                {issuesNeedYou}
              </span>
            )}
          </Button>
          <Button variant="ghost" size="icon" className="size-9 rounded-full text-muted-foreground" aria-label="Hub settings" title="Hub settings" onClick={onSettings}>
            <Settings className="size-4" />
          </Button>
          <Button variant="outline" size="icon" className="size-9 rounded-full" aria-label="Add agents" title="Add agents" onClick={onAdd}>
            <Plus className="size-4" />
          </Button>
        </div>
        <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground @[20rem]:pl-[42px]">
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
          {/* The hub's version keeps its room; the spend gives way first. */}
          {hubVersion && (
            <span className="shrink-0 font-mono text-[11px]" title={`The hub runs AgentAPI ${hubLabel(hubVersion)}`}>
              · {hubLabel(hubVersion)}
            </span>
          )}
          {/* Out of the live region: it changes every minute. */}
          {connected && spend.week > 0 && (
            <span className="min-w-0 truncate tabular-nums" title={spendTitle(spend.day, spend.week)}>
              · {formatUSDShort(spend.day)}/24h
            </span>
          )}
        </p>
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
            <kbd className="pointer-events-none absolute top-1/2 right-2.5 hidden -translate-y-1/2 rounded border bg-background px-1.5 font-mono text-[11px] text-muted-foreground @[20rem]:md:block">/</kbd>
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
