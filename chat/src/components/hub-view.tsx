"use client";

import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from "react";
import {Bell, BellOff, Plus, Search, X} from "lucide-react";
import {ModeToggle} from "@/components/mode-toggle";
import {Button} from "@/components/ui/button";
import {
  agentsIn,
  chatURL,
  countAgents,
  countFilters,
  displayName,
  formatUSD,
  groupNames,
  groupOf,
  hubRoot,
  matchesAgents,
  nothingMatches,
  parseAgents,
  shownAgents,
  spendTitle,
  spendTotals,
  type HubAgent,
  type HubFilter,
} from "@/lib/hub";
import {listSections, movePin, pinOf, sortPinned} from "@/lib/hub-order";
import {useStableCallback} from "@/lib/use-stable-callback";
import {cn} from "@/lib/utils";
import {HubMark} from "./hub/brand";
import {AddInstance, GroupName, type Naming} from "./hub/dialogs";
import {AgentChips, StatusChips} from "./hub/filter-chips";
import {HubClock} from "./hub/clock";
import {basePath, FrameInfo, InstanceBar} from "./hub/instance-bar";
import {GroupHeader, InstanceRow, type RowActions} from "./hub/instance-row";
import {ListEdge, useListWidth} from "./hub/list-edge";
import {ListOrderSwitch, useListOrder} from "./hub/list-order";
import {Onboarding, Overview} from "./hub/overview";
import {SignIn} from "./hub/sign-in";
import {readStorage, writeStorage} from "./hub/storage";
import {useHubTab} from "./hub/tab-icon";
import {cancelFromHub} from "./hub/update";
import {useHubActions} from "./hub/use-hub-actions";
import {useHubFrame} from "./hub/use-hub-frame";
import {useHubKeys} from "./hub/use-hub-keys";
import {useHubStream} from "./hub/use-hub-stream";
import {useNeedsYou} from "./hub/use-needs-you";

// Where the browser keeps which groups are folded, whether to notify, and
// the agents the list is filtered by.
const collapsedKey = "agentapi.hub.collapsed";
const notifyKey = "agentapi.hub.notify";
const agentsKey = "agentapi.hub.agents";

// HubView is the hub's page: sign in, the instances in a sidebar (what
// needs you first), and on the right either the overview or the chat of
// the one picked. On a narrow screen the list and the chat take turns.
export function HubView() {
  const root = useMemo(() => (typeof window === "undefined" ? "" : hubRoot(basePath, window.location.origin)), []);
  const {session, signedIn, retrySession, instances, hubVersion, unreachable, status} = useHubStream(root);
  const [selected, setSelected] = useState<string | undefined>(() =>
    typeof window === "undefined" ? undefined : decodeURIComponent(window.location.hash.slice(1)) || undefined,
  );
  const [filter, setFilter] = useState<HubFilter>("all");
  const [picked, setPicked] = useState<Set<HubAgent>>(() => parseAgents(readStorage<unknown>(agentsKey, [])));
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState(false);
  const [naming, setNaming] = useState<Naming | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set(readStorage<string[]>(collapsedKey, [])));
  const [notify, setNotify] = useState<boolean>(() => readStorage(notifyKey, false));
  const search = useRef<HTMLInputElement>(null);
  const listWidth = useListWidth();
  const [order, setOrder] = useListOrder();
  const connected = status === "connected";

  const all = useMemo(() => [...instances.values()], [instances]);
  const counts = useMemo(() => countFilters(all), [all]);
  const waiting = counts["needs-you"];
  const spend = useMemo(() => spendTotals(all), [all]);
  // The agents the instances run get chips once there are two; the list is
  // filtered by those picked that it has.
  const presentAgents = useMemo(() => agentsIn(all), [all]);
  const agents = useMemo(() => shownAgents(picked, presentAgents), [picked, presentAgents]);
  // Each row of chips counts what the other row lets through.
  const statusCounts = useMemo(() => (agents.size === 0 ? counts : countFilters(all.filter((instance) => matchesAgents(instance, agents)))), [all, agents, counts]);
  const agentCounts = useMemo(() => countAgents(all, filter), [all, filter]);
  const sections = useMemo(() => listSections(all, {filter, agents, query, order}), [all, filter, agents, query, order]);
  // The same list while the names are the same, so the rows don't render
  // again for it.
  const nameList = groupNames(all).join("\n");
  const names = useMemo(() => (nameList ? nameList.split("\n") : []), [nameList]);
  // The pinned rows the list shows, which Move up and Move down step past.
  const shownPins = sections[0]?.pinned ? sections[0].instances.map((instance) => instance.id) : [];

  // The row with the focus, by instance, or its menu's button. React moves
  // rows in the page when their order changes, and a row that moves loses
  // the focus: it gets it back, unless something else took it.
  const focusedRow = useRef<{id: string; menu: boolean} | undefined>(undefined);
  const rowOrder = sections.map((section) => section.instances.map((instance) => instance.id).join(" ")).join("\n");
  useLayoutEffect(() => {
    const focused = focusedRow.current;
    if (!focused || (document.activeElement && document.activeElement !== document.body)) return;
    const attribute = focused.menu ? "data-hub-row-menu" : "data-hub-row";
    document.querySelector<HTMLElement>(`[${attribute}="${CSS.escape(focused.id)}"]`)?.focus({preventScroll: true});
  }, [rowOrder]);

  // Opening an agent from the list is a page of its own in the browser's
  // history, so Back (a phone's back gesture) returns to the list; going
  // from one agent to another replaces it, and closing goes back.
  const select = useCallback((id: string | undefined) => {
    const opened = history.state?.hub === "agent";
    if (id) {
      const url = `#${encodeURIComponent(id)}`;
      if (opened) history.replaceState({hub: "agent"}, "", url);
      else history.pushState({hub: "agent"}, "", url);
    } else if (opened) {
      history.back();
      return;
    } else {
      history.replaceState(null, "", window.location.pathname + window.location.search);
    }
    setSelected(id);
  }, []);

  const {frame, framed} = useHubFrame({onBack: () => select(undefined), onCancelUpdate: (id) => void cancelFromHub(root, id)});

  // Back and forward show what the URL names.
  useEffect(() => {
    const onPop = () => setSelected(decodeURIComponent(window.location.hash.slice(1)) || undefined);
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  // Tell people who aren't looking: a screen reader, the tab, a notification.
  const announcement = useNeedsYou(all, notify, select);
  useHubTab(waiting, counts.working > 0);

  const actions = useHubActions(root, instances);
  useHubKeys({
    search,
    query,
    onClearSearch: () => setQuery(""),
    onOverview: () => select(undefined),
    onPin: (id) => {
      const instance = actions.instance(id);
      if (instance) void actions.setPinned(id, pinOf(instance) === undefined);
    },
  });

  const pickAgents = useCallback((next: Set<HubAgent>) => {
    setPicked(next);
    writeStorage(agentsKey, [...next]);
  }, []);

  const changeCollapsed = useCallback((change: (next: Set<string>) => void) => {
    setCollapsed((previous) => {
      const next = new Set(previous);
      change(next);
      writeStorage(collapsedKey, [...next]);
      return next;
    });
  }, []);

  const toggleNotify = async () => {
    if (notify) {
      setNotify(false);
      writeStorage(notifyKey, false);
      return;
    }
    if (typeof Notification === "undefined") return;
    const permission = Notification.permission === "default" ? await Notification.requestPermission() : Notification.permission;
    const on = permission === "granted";
    setNotify(on);
    writeStorage(notifyKey, on);
  };

  // Forget closes the agent once the hub forgot it.
  const forget = useStableCallback(async (id: string) => {
    if ((await actions.forget(id)) && selected === id) select(undefined);
  });
  // Moves a pinned instance past the next pinned row the list shows; the hub
  // keeps the order of all of them.
  const movePinned = useStableCallback(async (id: string, step: -1 | 1) => {
    const ids = movePin(sortPinned(all).map((instance) => instance.id), shownPins, id, step);
    if (ids) await actions.setPins(ids, id);
  });
  // What the rows do: one object for as long as the page is open.
  const rowActions = useMemo<RowActions>(
    () => ({
      select,
      forget: (id) => void forget(id),
      group: (id, name) => void actions.setGroup(id, name),
      newGroup: (id) => setNaming({mode: "new", id}),
      mute: (id, muted) => void actions.setMuted(id, muted),
      pin: (id, pinned) => void actions.setPinned(id, pinned),
      move: (id, step) => void movePinned(id, step),
    }),
    [select, forget, actions, movePinned],
  );
  const renameGroup = async (from: string, to: string) => {
    // A folded group stays folded under its new name.
    changeCollapsed((next) => {
      if (next.delete(from)) next.add(to);
    });
    await actions.moveGroup(
      all.filter((instance) => groupOf(instance) === from),
      to,
    );
  };

  if (session === undefined) {
    return (
      <main className="app-frame flex items-center justify-center bg-background">
        <HubMark className="size-10 animate-pulse" />
        <span className="sr-only">Loading the hub</span>
      </main>
    );
  }
  if (session === "unreachable") {
    return (
      <main className="app-frame flex flex-col items-center justify-center gap-4 bg-background px-6 text-center">
        <HubMark className="size-10 opacity-60" />
        <div role="status">
          <h1 className="text-base font-semibold">Can&apos;t reach the hub</h1>
          <p className="mt-1 text-sm text-muted-foreground">It didn&apos;t answer. Trying again…</p>
        </div>
        <Button variant="outline" size="sm" onClick={retrySession}>
          Try again
        </Button>
      </main>
    );
  }
  if (session === "signed-out") return <SignIn root={root} onSignedIn={signedIn} />;

  const current = selected ? instances.get(selected) : undefined;
  const filtered = filter !== "all" || agents.size > 0 || query.trim() !== "";
  return (
    <HubClock fast={counts.working > 0}>
      <main className="app-frame flex overflow-hidden bg-background">
        <aside
          id="hub-list"
          style={{"--hub-list-width": `${listWidth.width}px`} as React.CSSProperties}
          className={cn("relative flex w-full flex-col border-r bg-background md:w-(--hub-list-width) md:shrink-0", current ? "hidden md:flex" : "flex")}
        >
          <header className="space-y-3 border-b px-4 pt-[max(1rem,env(safe-area-inset-top))] pb-3">
            <div className="flex items-center gap-2.5">
              <HubMark className="size-8" />
              <div className="min-w-0 flex-1">
                <h1 className="text-[15px] leading-tight font-semibold tracking-tight">AgentAPI Hub</h1>
                <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                  <span
                    aria-hidden="true"
                    className={cn("size-1.5 shrink-0 rounded-full", connected ? "bg-state-ready" : "animate-pulse bg-state-needs")}
                  />
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
                onClick={() => void toggleNotify()}
                aria-pressed={notify}
                aria-label={notify ? "Stop notifying me" : "Notify me when an agent needs me"}
                title={notify ? "Notifications on" : "Notify me when an agent needs me"}
              >
                {notify ? <Bell className="size-4 text-foreground" /> : <BellOff className="size-4" />}
              </Button>
              <ModeToggle />
              <Button variant="outline" size="icon" className="size-9 rounded-full" aria-label="Add agents" title="Add agents" onClick={() => setAdding(true)}>
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
              <ListOrderSwitch order={order} onChange={setOrder} />
            </div>
            <div className="space-y-2">
              <StatusChips filter={filter} counts={statusCounts} onChange={setFilter} />
              {presentAgents.length > 1 && <AgentChips agents={presentAgents} picked={agents} counts={agentCounts} onChange={pickAgents} />}
            </div>
          </header>

          <ul
            className="flex-1 overflow-y-auto overscroll-contain pb-[max(0.75rem,env(safe-area-inset-bottom))]"
            aria-label="Instances"
            data-hub-list
            onFocus={(event) => {
              const {hubRow, hubRowMenu} = (event.target as HTMLElement).dataset;
              focusedRow.current = hubRow ? {id: hubRow, menu: false} : hubRowMenu ? {id: hubRowMenu, menu: true} : undefined;
            }}
            onBlur={(event) => {
              // Not when the row left the page with the focus (relatedTarget is
              // none then): it gets it back.
              if (event.relatedTarget) focusedRow.current = undefined;
            }}
          >
            {counts.all === 0 && (
              <li>
                <div className="md:hidden">
                  <Onboarding root={root} onAdd={() => setAdding(true)} compact />
                </div>
                <div className="hidden px-6 py-12 text-center md:block">
                  <HubMark className="mx-auto size-10 opacity-60" />
                  <p className="mt-4 text-sm font-medium">No agents yet</p>
                  <p className="mt-1 text-sm text-muted-foreground">The steps on the right connect them.</p>
                </div>
              </li>
            )}
            {counts.all > 0 && sections.length === 0 && (
              <li className="px-6 py-12 text-center">
                <p className="text-sm font-medium">Nothing matches</p>
                <p className="mt-1 text-sm text-muted-foreground">
                  {nothingMatches(query, filter, agents)}.
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="mt-4"
                  onClick={() => {
                    setFilter("all");
                    pickAgents(new Set());
                    setQuery("");
                  }}
                >
                  Show all
                </Button>
              </li>
            )}
            {sections.map((section) => {
              // A search or a filter shows every match, folded or not.
              const folded = !!section.label && !filtered && collapsed.has(section.key);
              // Each section is a block of its own, so its header sticks only
              // while its rows scroll by, and the next header pushes it off.
              return (
                <li key={`section:${section.key}`}>
                  {section.label && (
                    <GroupHeader
                      group={section}
                      label={section.label}
                      folded={folded}
                      onToggle={() =>
                        changeCollapsed((next) => {
                          if (!next.delete(section.key)) next.add(section.key);
                        })
                      }
                      onRename={() => setNaming({mode: "rename", from: section.name})}
                    />
                  )}
                  {!folded && (
                    <ul aria-label={section.label || undefined}>
                      {section.instances.map((instance, index) => (
                        <InstanceRow
                          key={instance.id}
                          instance={instance}
                          hubVersion={hubVersion}
                          root={root}
                          groups={names}
                          order={order}
                          selected={instance.id === selected}
                          pinned={section.pinned ? {first: index === 0, last: index === section.instances.length - 1} : undefined}
                          actions={rowActions}
                        />
                      ))}
                    </ul>
                  )}
                </li>
              );
            })}
          </ul>
          <p className="hidden border-t px-4 py-2 text-[11px] text-muted-foreground md:block" aria-hidden="true">
            <kbd className="font-mono">/</kbd> search · <kbd className="font-mono">j</kbd> <kbd className="font-mono">k</kbd> move ·{" "}
            <kbd className="font-mono">↵</kbd> open · <kbd className="font-mono">p</kbd> pin · <kbd className="font-mono">esc</kbd> overview
          </p>
          <ListEdge controls="hub-list" {...listWidth} />
        </aside>

        <section className={cn("min-w-0 flex-1 flex-col bg-[color-mix(in_oklch,var(--muted)_45%,var(--background))]", current ? "flex" : "hidden md:flex")}>
          {current ? (
            <>
              {framed?.instance === current.id ? (
                <FrameInfo frame={frame} ready={framed.ready} instance={current} hubVersion={hubVersion} root={root} />
              ) : (
                <InstanceBar instance={current} hubVersion={hubVersion} root={root} onBack={() => select(undefined)} />
              )}
              <iframe
                ref={frame}
                key={current.id}
                data-instance={current.id}
                title={`${displayName(current)} chat`}
                src={chatURL(basePath, root, current.id, true)}
                className={cn("min-h-0 w-full flex-1 border-0 bg-background", framed?.instance === current.id && "pt-[env(safe-area-inset-top)]")}
              />
            </>
          ) : (
            <Overview
              root={root}
              instances={all}
              hubVersion={hubVersion}
              counts={counts}
              onOpen={select}
              onFilter={setFilter}
              onAdd={() => setAdding(true)}
            />
          )}
        </section>

        {/* Assertive: an agent that needs you. role="status" would make it polite. */}
        <div className="sr-only" aria-live="assertive" aria-atomic="true">
          {announcement}
        </div>
        <AddInstance root={root} open={adding} onOpenChange={setAdding} onAdded={(id) => select(id)} />
        <GroupName
          naming={naming}
          onClose={() => setNaming(null)}
          onSave={(name) => {
            if (naming?.mode === "new") void actions.setGroup(naming.id, name);
            else if (naming?.mode === "rename") void renameGroup(naming.from, name);
            setNaming(null);
          }}
        />
      </main>
    </HubClock>
  );
}
