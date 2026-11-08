"use client";

import {memo, useCallback, useEffect, useId, useRef, useState} from "react";
import {Folder, Server} from "lucide-react";
import {displayName, hasRun, instanceState, stateLabel, usagePercent, type HubInstance} from "@/lib/hub";
import {activity, formatAgo} from "@/lib/hub-format";
import {groupOf} from "@/lib/hub-groups";
import {activeAgent, sharedVersion, tabOrder, type HubMachine} from "@/lib/hub-machines";
import {formatUSD, spendTitle, spendTotals, tokenTotals} from "@/lib/hub-spend";
import {instanceVersion, olderThanHub} from "@/lib/hub-version";
import {changedAt, recentAgent, type ListOrder} from "@/lib/hub-order";
import {MAX_TAB_PICKS, readHubSetting, writeHubSetting, type TabPick} from "@/lib/hub-settings";
import {shownUpdate, updateBadge} from "@/lib/hub-update";
import {cn} from "@/lib/utils";
import {useHubTime} from "./clock";
import {activityTone, agentName, RowMenu, RowWork, UsageBar, type RowActions} from "./instance-row";
import {AgentTabMark, shortAgent} from "./machine-tabs";
import {stateTone} from "./status-glyph";
import {UpdateChip} from "./update-chip";
import {VersionTag} from "./version-tag";

export interface MachineCardProps {
  // card tells the card from the others, as it remembers the tab picked:
  // a machine with agents in two groups has a card in each.
  card: string;
  machine: HubMachine;
  // prefix is the start most machines' names share (hostPrefix), dimmed.
  prefix: string;
  // order is the list's: in the recent order the card shows its latest
  // change, and its agents' changes.
  order: ListOrder;
  // hubVersion is the hub's own version, which marks the older instances.
  hubVersion: string;
  // root is the hub's address, whose proxy takes the instance's update.
  root: string;
  groups: string[];
  // selected is the instance open beside the list, if any.
  selected?: string;
  actions: RowActions;
}

// keepPick remembers the tab picked on a card, in the browser, the latest
// last, so a reload shows the same ones; it forgets the oldest past
// MAX_TAB_PICKS.
function keepPick(card: string, pick: TabPick) {
  const kept = new Map(readHubSetting("tabPicks"));
  const was = kept.get(card);
  if (was?.id === pick.id && was.at === pick.at) return;
  kept.delete(card);
  kept.set(card, pick);
  for (const oldest of kept.keys()) {
    if (kept.size <= MAX_TAB_PICKS) break;
    kept.delete(oldest);
  }
  writeHubSetting("tabPicks", kept);
}

// useActiveAgent is the agent a card shows (activeAgent; in the recent
// order, recentAgent), and picks another: the one picked last on it, until
// the agent open beside the list becomes one of its own.
function useActiveAgent(card: string, machine: HubMachine, order: ListOrder, selected?: string): [string, (id: string) => void] {
  const onCard = selected && machine.instances.some((instance) => instance.id === selected) ? selected : undefined;
  const [picked, setPicked] = useState<TabPick | undefined>(() => (onCard ? {id: onCard, at: Date.now()} : readHubSetting("tabPicks").get(card)));
  const [opened, setOpened] = useState(onCard);
  if (onCard !== opened) {
    setOpened(onCard);
    if (onCard) setPicked({id: onCard, at: Date.now()});
  }
  useEffect(() => {
    if (picked) keepPick(card, picked);
  }, [card, picked]);
  const pick = useCallback((id: string) => setPicked({id, at: Date.now()}), []);
  return [order === "recent" ? recentAgent(machine, picked, onCard) : activeAgent(machine, picked?.id, onCard), pick];
}

// splitHost parts a machine's name into the start it shares with most
// machines and the rest; all of it is the rest when it doesn't start so.
function splitHost(hostname: string, prefix: string): {start: string; rest: string} {
  return prefix && hostname.startsWith(prefix) && hostname.length > prefix.length ? {start: prefix, rest: hostname.slice(prefix.length)} : {start: "", rest: hostname};
}

// MachineCard is a machine in the hub's list, by machine: its name (in the
// recent order, with how long ago the latest of its agents changed), a tab
// per agent with what they share beside them (the AgentAPI version, what
// they spent in the last 24 hours), and the agent of the tab picked
// (AgentPanel).
// A tab opens its agent; the arrows move between the tabs, and on the panel
// switch it without opening.
export function MachineCard({card, machine, prefix, order, hubVersion, root, groups, selected, actions}: MachineCardProps) {
  const {hostname, instances} = machine;
  const version = sharedVersion(instances);
  const lastKnown = instances.every((instance) => instance.connection !== "connected");
  const spend = spendTotals(instances);
  const tokens = tokenTotals(instances);
  const spent = spendTitle(spend.day, spend.week, false, tokens.counted > 0 ? tokens.day.total : undefined);
  const [active, pick] = useActiveAgent(card, machine, order, selected);
  const recent = order === "recent";
  // The recent order puts the machine of the latest change first: the age
  // of its first agent's.
  const latest = useHubTime((now) => (recent ? formatAgo(changedAt(instances[0]), now) : ""));
  const tabs = tabOrder(instances);
  const single = tabs.length === 1;
  const shown = splitHost(hostname, prefix);
  const current = instances.find((instance) => instance.id === active)!;
  const id = useId();
  const list = useRef<HTMLDivElement>(null);
  const tabID = (agent: string) => `${id}-tab-${tabs.findIndex((tab) => tab.id === agent)}`;
  // step picks the tab before (-1) or after (1) the active one, round the
  // ends; focus moves to it when the focus is on the tabs.
  const step = (by: -1 | 1 | "first" | "last", focus: boolean) => {
    const at = tabs.findIndex((tab) => tab.id === active);
    const next = by === "first" ? 0 : by === "last" ? tabs.length - 1 : (at + by + tabs.length) % tabs.length;
    pick(tabs[next].id);
    if (focus) list.current?.querySelectorAll<HTMLElement>("[role=tab]")[next]?.focus();
  };
  const keys = {ArrowLeft: -1, ArrowRight: 1, Home: "first", End: "last"} as const;
  return (
    <li className="px-2 py-1" data-hub-card>
      <div className="rounded-xl border bg-card/60">
        <div className="flex min-w-0 items-start gap-2 px-3.5 pt-2.5 text-xs leading-5 text-muted-foreground">
          <Server aria-hidden="true" className="mt-[3px] size-3.5 shrink-0" />
          {/* The name has the line to itself and wraps rather than losing its
              end; the start most machines share is dimmed. */}
          <span className="min-w-0 flex-1 text-[13px] font-medium [overflow-wrap:anywhere] text-foreground" title={hostname} data-hub-host>
            {shown.start && <span className="font-normal text-muted-foreground">{shown.start}</span>}
            {shown.rest}
          </span>
          {latest && (
            <span className="shrink-0 tabular-nums" title="The latest change of its agents">
              {latest}
            </span>
          )}
        </div>
        {/* The agents' tabs, and what they share on the right. */}
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-2 pt-1 pb-1">
          <div
            ref={list}
            role="tablist"
            aria-label={`Agents on ${hostname}`}
            className="flex min-w-0 flex-wrap gap-1"
            onKeyDown={(event) => {
              const by = keys[event.key as keyof typeof keys];
              if (by === undefined) return;
              event.preventDefault();
              step(by, true);
            }}
          >
            {tabs.map((instance) => {
              const on = instance.id === active;
              const agent = agentName(instance) || "Agent";
              return (
                <button
                  key={instance.id}
                  type="button"
                  role="tab"
                  id={tabID(instance.id)}
                  aria-selected={on}
                  aria-controls={`${id}-panel`}
                  tabIndex={on ? 0 : -1}
                  aria-label={`${agent}, ${displayName(instance)}, ${stateLabel(instance)}`}
                  title={`${agent}, ${displayName(instance)}: ${stateLabel(instance)}`}
                  data-hub-tab={instance.id}
                  onClick={() => {
                    pick(instance.id);
                    actions.select(instance.id);
                  }}
                  className={cn(
                    "flex h-7 min-w-0 items-center gap-1.5 rounded-md px-2 text-xs outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
                    // A machine's only agent shown is a label rather than a tab to pick.
                  single ? "-ml-0.5 px-1 font-medium text-foreground" : on ? "bg-background font-medium text-foreground shadow-xs ring-1 ring-border dark:bg-accent" : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                  )}
                >
                  <AgentTabMark agentType={instance.agent_type} state={instanceState(instance)} ran={hasRun(instance)} />
                  {/* The other tabs are their marks, so the tabs and what the
                    agents share fit one line; the label says them in full. */}
                {(on || single) && <span className="truncate">{shortAgent(instance.agent_type, agent)}</span>}
                </button>
              );
            })}
          </div>
          <span className="ml-auto flex shrink-0 items-center gap-2 text-xs leading-7 text-muted-foreground">
            {version && <VersionTag version={version} hubVersion={hubVersion} lastKnown={lastKnown} />}
            {/* The only agent's spend is its panel's. */}
            {!single && spend.week > 0 && (
              <span className="tabular-nums" title={spent}>
                <span className="sr-only">{spent}</span>
                <span aria-hidden="true">
                  {formatUSD(spend.day)}
                  <span className="text-muted-foreground/60">/24h</span>
                </span>
              </span>
            )}
          </span>
        </div>
        <div role="tabpanel" id={`${id}-panel`} aria-labelledby={tabID(active)} className="px-1 pb-1">
          <AgentPanel
            instance={current}
            hubVersion={hubVersion}
            root={root}
            groups={groups}
            named={!hostname.toLowerCase().includes(displayName(current).toLowerCase())}
            versioned={!version}
            selected={current.id === selected}
            recent={recent}
            latest={current === instances[0]}
            actions={actions}
            onStep={(by) => step(by, false)}
          />
        </div>
      </div>
    </li>
  );
}

interface AgentPanelProps {
  instance: HubInstance;
  hubVersion: string;
  root: string;
  groups: string[];
  // named: the panel names its instance, as the machine's name doesn't say
  // it; versioned: it shows its version, as its machine's agents don't
  // share one.
  named: boolean;
  versioned: boolean;
  selected: boolean;
  // recent: the list's order is the recent one, where the panel's age is
  // that of its agent's latest change, as a row's is there, and it names
  // its group; none for the card's latest, whose age the header shows.
  recent: boolean;
  latest: boolean;
  actions: RowActions;
  // onStep shows the agent of the tab before (-1) or after (1).
  onStep: (by: -1 | 1) => void;
}

// AgentPanel is the agent of a MachineCard's tab: what it does, what it
// spent and worked, opened as an InstanceRow is, with the same menu. The
// list's keys step to it as to a row; the left and right arrows show the
// card's other agents.
const AgentPanel = memo(
  function AgentPanel({instance, hubVersion, root, groups, named, versioned, selected, recent, latest, actions, onStep}: AgentPanelProps) {
    const state = instanceState(instance);
    const summary = instance.summary;
    const usage = usagePercent(summary);
    const usageShown = usage !== undefined && usage >= 60;
    const agent = agentName(instance) || "Agent";
    const doing = activity(instance);
    const ago = useHubTime((now) => (recent && latest ? "" : formatAgo(recent ? changedAt(instance) : doing.since, now)));
    const group = recent ? groupOf(instance) : "";
    const spend = summary?.spend;
    const lastKnown = instance.connection !== "connected";
    const version = instanceVersion(instance);
    const update = shownUpdate(instance);
    const id = instance.id;
    return (
      <div className="group/row relative">
        <div className={cn("flex items-stretch rounded-lg transition-colors", selected ? "bg-accent ring-1 ring-border" : "hover:bg-accent/60")}>
          {selected && <span aria-hidden="true" className="absolute inset-y-2.5 left-0 w-[3px] rounded-r-full bg-state-working" />}
          <button
            type="button"
            data-hub-row={id}
            onClick={() => actions.select(id)}
            onKeyDown={(event) => {
              if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
              event.preventDefault();
              onStep(event.key === "ArrowLeft" ? -1 : 1);
            }}
            aria-current={selected ? "true" : undefined}
            aria-label={`${agent}, ${displayName(instance)}, ${stateLabel(instance)}${ago ? `, ${ago}` : ""}: ${doing.text}${olderThanHub(instance, hubVersion) ? ". Older than the hub" : ""}${update ? `. ${updateBadge(update).title}` : ""}`}
            className="flex min-w-0 flex-1 flex-col rounded-lg py-1.5 pr-1 pl-2.5 text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
          >
            <span className={cn("block w-full truncate text-[13px] leading-5", activityTone[doing.tone])}>
              {doing.label && (
                <>
                  <span className={cn("font-medium", stateTone[state])}>{doing.label}</span>
                  <span className="text-muted-foreground/60"> · </span>
                </>
              )}
              {doing.text}
            </span>
            <span className="flex w-full min-w-0 items-center gap-2 text-xs leading-5 text-muted-foreground">
              {named && (
                <span className="min-w-0 truncate" title={displayName(instance)}>
                  {displayName(instance)}
                </span>
              )}
              {versioned && version && <VersionTag version={version} hubVersion={hubVersion} lastKnown={lastKnown} />}
              {summary && summary.queued_messages > 0 && (
                <span className="shrink-0 rounded-full bg-muted px-1.5 py-px font-medium text-foreground/80">{summary.queued_messages} queued</span>
              )}
              {spend && spend.week_usd > 0 && (
                <span
                  className={cn("shrink-0 tabular-nums", lastKnown && "opacity-55")}
                  title={spendTitle(spend.day_usd, spend.week_usd, lastKnown, spend.day_tokens?.total)}
                  data-spend-last-known={lastKnown || undefined}
                >
                  <span className="sr-only">{spendTitle(spend.day_usd, spend.week_usd, lastKnown, spend.day_tokens?.total)}</span>
                  <span aria-hidden="true">
                    {formatUSD(spend.day_usd)}
                    <span className="text-muted-foreground/60">/24h</span>
                  </span>
                </span>
              )}
              {/* The usage limit's bar needs the room more. */}
              {usageShown ? <UsageBar percent={usage} /> : <RowWork instance={instance} />}
              {group && (
                <span className="min-w-0 truncate" title={`Group: ${group}`}>
                  <Folder className="mr-1 inline size-3 align-[-2px]" aria-hidden="true" />
                  {group}
                </span>
              )}
              {ago && <span className="ml-auto shrink-0 tabular-nums">{ago}</span>}
            </span>
            {update && (
              <span className="mt-1 flex text-xs">
                <UpdateChip update={update} />
              </span>
            )}
          </button>
          <RowMenu instance={instance} hubVersion={hubVersion} root={root} groups={groups} actions={actions} className="size-8" />
        </div>
      </div>
    );
  },
  (a, b) =>
    a.instance === b.instance &&
    a.hubVersion === b.hubVersion &&
    a.root === b.root &&
    a.groups === b.groups &&
    a.named === b.named &&
    a.versioned === b.versioned &&
    a.selected === b.selected &&
    a.recent === b.recent &&
    a.latest === b.latest &&
    a.actions === b.actions,
);
