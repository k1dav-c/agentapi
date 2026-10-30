"use client";

import {useCallback, useEffect, useMemo, useRef, useState} from "react";
import {ArrowLeft, Bell, BellOff, ExternalLink, Plus, Search, X} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {ModeToggle} from "@/components/mode-toggle";
import {Button} from "@/components/ui/button";
import {
  activity,
  applyHubEvent,
  chatURL,
  countFilters,
  displayName,
  formatUSD,
  groupInstances,
  groupNames,
  hubRoot,
  instanceState,
  matchesFilter,
  matchesSearch,
  spendTitle,
  spendTotals,
  stateLabel,
  type HubEvent,
  type HubFilter,
  type HubInstance,
  type InstanceState,
} from "@/lib/hub";
import {frameMessage, toFrame, type FromFrame} from "@/lib/hub-frame";
import {checkHubSession, type HubSession} from "@/lib/hub-session";
import {getReconnectDelay} from "@/lib/reconnect";
import {cn} from "@/lib/utils";
import {useEventSource} from "./use-event-source";
import {HubMark} from "./hub/brand";
import {AddInstance, GroupName, type Naming} from "./hub/dialogs";
import {GroupHeader, InstanceRow, agentName} from "./hub/instance-row";
import {Onboarding, Overview} from "./hub/overview";
import {SignIn} from "./hub/sign-in";
import {StatusGlyph, stateTone} from "./hub/status-glyph";

const basePath = (process.env.NEXT_PUBLIC_BASE_PATH ?? "/chat").replace(/\/$/, "");

// Where the browser keeps which groups are folded, and whether to notify.
const collapsedKey = "agentapi.hub.collapsed";
const notifyKey = "agentapi.hub.notify";
// The hub sends a heartbeat every 15 s.
const STALE_STREAM_MS = 45_000;

const filters: {id: HubFilter; label: string}[] = [
  {id: "all", label: "All"},
  {id: "needs-you", label: "Need you"},
  {id: "working", label: "Working"},
  {id: "unread", label: "Unseen"},
  {id: "offline", label: "Offline"},
];

function readStorage<T>(key: string, fallback: T): T {
  try {
    const value = localStorage.getItem(key);
    return value === null ? fallback : (JSON.parse(value) as T);
  } catch {
    return fallback;
  }
}

function writeStorage(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {}
}

// useNow ticks: every second while something runs (for the running
// times), else every half minute (for the ages).
function useNow(fast: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), fast ? 1000 : 30_000);
    return () => clearInterval(timer);
  }, [fast]);
  return now;
}

// The tab icon: the hub's mark, with a dot in the most pressing state and
// the number that need you.
function faviconUrl(waiting: number, working: boolean): string {
  const dot = waiting > 0 ? "#c27c0e" : working ? "#4f6bed" : "";
  const badge = dot
    ? `<circle cx="24" cy="8" r="7.5" fill="${dot}" stroke="#ffffff" stroke-width="1.5"/>` +
      (waiting > 0 ? `<text x="24" y="11.4" font-family="system-ui,sans-serif" font-size="10" font-weight="700" fill="#ffffff" text-anchor="middle">${waiting > 9 ? "9+" : waiting}</text>` : "")
    : "";
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="9" fill="#232733"/>` +
    `<circle cx="16" cy="16" r="3.6" fill="#ffffff"/><circle cx="9.3" cy="9.3" r="2.2" fill="#c27c0e"/><circle cx="24.6" cy="20" r="2.2" fill="#4f6bed"/><circle cx="12" cy="25" r="2.2" fill="#16a07a"/>` +
    badge +
    `</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

// HubView is the hub's page: sign in, the instances in a sidebar (what
// needs you first), and on the right either the overview or the chat of
// the one picked. On a narrow screen the list and the chat take turns.
export function HubView() {
  const root = useMemo(() => (typeof window === "undefined" ? "" : hubRoot(basePath, window.location.origin)), []);
  const [session, setSession] = useState<HubSession | undefined>(undefined);
  const [instances, setInstances] = useState<Map<string, HubInstance>>(new Map());
  // The last session check after the stream failed found the hub
  // unreachable.
  const [unreachable, setUnreachable] = useState(false);
  const [selected, setSelected] = useState<string | undefined>(() =>
    typeof window === "undefined" ? undefined : decodeURIComponent(window.location.hash.slice(1)) || undefined,
  );
  const [filter, setFilter] = useState<HubFilter>("all");
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState(false);
  const [naming, setNaming] = useState<Naming | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set(readStorage<string[]>(collapsedKey, [])));
  const [notify, setNotify] = useState<boolean>(() => readStorage(notifyKey, false));
  const [announcement, setAnnouncement] = useState("");
  const search = useRef<HTMLInputElement>(null);

  // Signed in or not; a hub that doesn't answer is asked again, with a
  // backoff, and at once when the browser comes back online.
  const [sessionAttempt, setSessionAttempt] = useState(0);
  useEffect(() => {
    if (!root) return;
    let active = true;
    void checkHubSession(root).then((result) => {
      if (active) setSession(result);
    });
    return () => {
      active = false;
    };
  }, [root, sessionAttempt]);
  useEffect(() => {
    if (session !== "unreachable") return;
    const retry = () => setSessionAttempt((n) => n + 1);
    const timer = setTimeout(retry, getReconnectDelay(sessionAttempt + 1));
    window.addEventListener("online", retry);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("online", retry);
    };
  }, [session, sessionAttempt]);

  // The instances and their changes, from one stream, kept open.
  const stream = useEventSource({
    url: session === "signed-in" && root ? `${root}/api/events` : null,
    withCredentials: true,
    staleAfter: STALE_STREAM_MS,
    listeners: () => {
      const on = (type: HubEvent["type"]) => (message: MessageEvent<string>) => {
        const data = JSON.parse(message.data);
        const event: HubEvent =
          type === "instances" ? {type, instances: data.instances ?? []} : type === "instance" ? {type, instance: data} : {type, id: data.id};
        setInstances((previous) => applyHubEvent(previous, event));
      };
      return {instances: on("instances"), instance: on("instance"), removed: on("removed")};
    },
    onOpen: () => setUnreachable(false),
    // EventSource hides the status: a session that expired comes back as a
    // sign-in, a hub that doesn't answer as unreachable.
    onError: () => {
      void checkHubSession(root).then((result) => {
        if (result === "signed-out") setSession("signed-out");
        setUnreachable(result === "unreachable");
      });
    },
  });
  const connected = stream.status === "connected";

  const all = useMemo(() => [...instances.values()], [instances]);
  const counts = useMemo(() => countFilters(all), [all]);
  const waiting = counts["needs-you"];
  const now = useNow(counts.working > 0);
  const visible = useMemo(() => all.filter((i) => matchesFilter(i, filter) && matchesSearch(i, query)), [all, filter, query]);
  const spend = useMemo(() => spendTotals(all), [all]);
  const groups = useMemo(() => groupInstances(visible), [visible]);
  const names = useMemo(() => groupNames(all), [all]);
  const showHeaders = names.length > 0;

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

  // The open instance's chat draws the bar above itself once it is ready
  // (FrameBar), with its own actions in it; until then the hub's bar shows.
  const frame = useRef<HTMLIFrameElement>(null);
  const [framed, setFramed] = useState<string>();
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const chat = frame.current;
      if (!chat || event.source !== chat.contentWindow) return;
      const message = frameMessage<FromFrame>(event);
      if (message?.type === "ready") setFramed(chat.dataset.instance);
      else if (message?.type === "back") select(undefined);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [select]);

  // Back and forward show what the URL names.
  useEffect(() => {
    const onPop = () => setSelected(decodeURIComponent(window.location.hash.slice(1)) || undefined);
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  // Tell people who aren't looking: a screen reader, the tab, a notification.
  const previousStates = useRef<Map<string, InstanceState>>(new Map());
  useEffect(() => {
    const before = previousStates.current;
    const next = new Map<string, InstanceState>();
    for (const instance of all) {
      const state = instanceState(instance);
      next.set(instance.id, state);
      if (state === "needs-you" && before.size > 0 && before.get(instance.id) !== "needs-you") {
        const text = `${displayName(instance)}: ${activity(instance).text}`;
        setAnnouncement(`${displayName(instance)} needs you. ${activity(instance).text}`);
        if (notify && document.hidden && typeof Notification !== "undefined" && Notification.permission === "granted") {
          const notification = new Notification(`${displayName(instance)} needs you`, {body: text, tag: `agentapi-hub-${instance.id}`});
          notification.onclick = () => {
            window.focus();
            select(instance.id);
            notification.close();
          };
        }
      }
    }
    previousStates.current = next;
  }, [all, notify, select]);

  useEffect(() => {
    document.title = waiting > 0 ? `(${waiting}) AgentAPI Hub` : "AgentAPI Hub";
    let link = document.querySelector<HTMLLinkElement>("link[data-hub-icon]");
    if (!link) {
      document.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]').forEach((icon) => icon.remove());
      link = document.createElement("link");
      link.rel = "icon";
      link.type = "image/svg+xml";
      link.setAttribute("data-hub-icon", "");
      document.head.appendChild(link);
    }
    link.href = faviconUrl(waiting, counts.working > 0);
  }, [waiting, counts.working]);

  // Keys: / searches, j and k (or the arrows) move between rows, Esc goes
  // back to the overview.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const typing = target?.closest("input, textarea, [contenteditable=true]");
      if (event.key === "/" && !typing) {
        event.preventDefault();
        search.current?.focus();
        return;
      }
      if (event.key === "Escape") {
        if (typing === search.current && query) setQuery("");
        else if (!document.querySelector("[role=dialog], [role=menu]")) select(undefined);
        return;
      }
      if (typing || event.metaKey || event.ctrlKey || event.altKey) return;
      const step = event.key === "j" || event.key === "ArrowDown" ? 1 : event.key === "k" || event.key === "ArrowUp" ? -1 : 0;
      if (!step) return;
      const rows = [...document.querySelectorAll<HTMLButtonElement>("[data-hub-row]")];
      if (rows.length === 0) return;
      event.preventDefault();
      const at = rows.indexOf(document.activeElement as HTMLButtonElement);
      const next = rows[at === -1 ? (step > 0 ? 0 : rows.length - 1) : Math.min(rows.length - 1, Math.max(0, at + step))];
      next.focus();
      next.scrollIntoView({block: "nearest"});
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [query, select]);

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

  const forget = async (id: string) => {
    await fetch(`${root}/api/instances/${encodeURIComponent(id)}`, {method: "DELETE", credentials: "same-origin"});
    if (selected === id) select(undefined);
  };
  // The change comes back on the event stream, like any other.
  const setGroup = async (id: string, group: string) => {
    await fetch(`${root}/api/instances/${encodeURIComponent(id)}`, {
      method: "PATCH",
      credentials: "same-origin",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({group}),
    });
  };
  // Mutes what an instance waits for now, until that changes (the hub ends
  // the mute then), or ends the mute.
  const setMuted = async (id: string, muted: boolean) => {
    await fetch(`${root}/api/instances/${encodeURIComponent(id)}`, {
      method: "PATCH",
      credentials: "same-origin",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({muted}),
    });
  };
  const renameGroup = async (from: string, to: string) => {
    // A folded group stays folded under its new name.
    changeCollapsed((next) => {
      if (next.delete(from)) next.add(to);
    });
    const members = all.filter((instance) => (instance.group?.trim() ?? "") === from);
    await Promise.all(members.map((instance) => setGroup(instance.id, to)));
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
        <Button variant="outline" size="sm" onClick={() => setSessionAttempt((n) => n + 1)}>
          Try again
        </Button>
      </main>
    );
  }
  if (session === "signed-out") return <SignIn root={root} onSignedIn={() => setSession("signed-in")} />;

  const current = selected ? instances.get(selected) : undefined;
  const filtered = filter !== "all" || query.trim() !== "";
  return (
    <main className="app-frame flex overflow-hidden bg-background">
      <aside className={cn("flex w-full flex-col border-r bg-background md:w-[360px] md:shrink-0", current ? "hidden md:flex" : "flex")}>
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
                    : stream.status === "offline"
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
            <Button variant="outline" size="icon" className="size-9 rounded-full" aria-label="Add an instance" title="Add an instance" onClick={() => setAdding(true)}>
              <Plus className="size-4" />
            </Button>
          </div>
          <div className="relative">
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
          <div
            role="group"
            aria-label="Filter the agents"
            className="-mx-4 flex gap-1.5 overflow-x-auto px-4 pb-0.5 [mask-image:linear-gradient(to_right,black_calc(100%-28px),transparent)] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          >
            {filters.map((f) => {
              const active = filter === f.id;
              const count = counts[f.id];
              return (
                <button
                  key={f.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setFilter(active && f.id !== "all" ? "all" : f.id)}
                  className={cn(
                    "flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-3 text-xs font-medium outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
                    active ? "border-foreground bg-foreground text-background" : "bg-background text-muted-foreground hover:text-foreground",
                    !active && f.id === "needs-you" && count > 0 && "border-state-needs/40 text-state-needs",
                  )}
                >
                  {f.label}
                  <span className={cn("tabular-nums", active ? "text-background/70" : "text-muted-foreground/70")}>{count}</span>
                </button>
              );
            })}
          </div>
        </header>

        <ul className="flex-1 overflow-y-auto overscroll-contain pb-[max(0.75rem,env(safe-area-inset-bottom))]" aria-label="Instances">
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
          {counts.all > 0 && visible.length === 0 && (
            <li className="px-6 py-12 text-center">
              <p className="text-sm font-medium">Nothing matches</p>
              <p className="mt-1 text-sm text-muted-foreground">
                {query ? `No agent matches “${query.trim()}”` : "No agent is in this state right now"}.
              </p>
              <Button
                variant="outline"
                size="sm"
                className="mt-4"
                onClick={() => {
                  setFilter("all");
                  setQuery("");
                }}
              >
                Show all
              </Button>
            </li>
          )}
          {groups.map((group) => {
            // A search or a filter shows every match, folded or not.
            const folded = showHeaders && !filtered && collapsed.has(group.name);
            // Each group is a block of its own, so its header sticks only
            // while its rows scroll by, and the next header pushes it off.
            return (
              <li key={group.name || "\u0000"}>
                {showHeaders && (
                  <GroupHeader
                    group={group}
                    folded={folded}
                    onToggle={() =>
                      changeCollapsed((next) => {
                        if (!next.delete(group.name)) next.add(group.name);
                      })
                    }
                    onRename={() => setNaming({mode: "rename", from: group.name})}
                  />
                )}
                {!folded && (
                  <ul aria-label={group.name || (showHeaders ? "Ungrouped" : undefined)}>
                    {group.instances.map((instance) => (
                      <InstanceRow
                        key={instance.id}
                        instance={instance}
                        groups={names}
                        now={now}
                        selected={instance.id === selected}
                        onSelect={() => select(instance.id)}
                        onForget={() => void forget(instance.id)}
                        onGroup={(name) => void setGroup(instance.id, name)}
                        onNewGroup={() => setNaming({mode: "new", id: instance.id})}
                        onMute={(muted) => void setMuted(instance.id, muted)}
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
          <kbd className="font-mono">↵</kbd> open · <kbd className="font-mono">esc</kbd> overview
        </p>
      </aside>

      <section className={cn("min-w-0 flex-1 flex-col bg-[color-mix(in_oklch,var(--muted)_45%,var(--background))]", current ? "flex" : "hidden md:flex")}>
        {current ? (
          <>
            {framed === current.id ? <FrameInfo frame={frame} instance={current} root={root} /> : <InstanceBar instance={current} root={root} onBack={() => select(undefined)} />}
            <iframe
              ref={frame}
              key={current.id}
              data-instance={current.id}
              title={`${displayName(current)} chat`}
              src={chatURL(basePath, root, current.id, true)}
              className={cn("min-h-0 w-full flex-1 border-0 bg-background", framed === current.id && "pt-[env(safe-area-inset-top)]")}
            />
          </>
        ) : (
          <Overview root={root} instances={all} counts={counts} now={now} onOpen={select} onFilter={setFilter} onAdd={() => setAdding(true)} />
        )}
      </section>

      <div className="sr-only" aria-live="assertive" role="status">
        {announcement}
      </div>
      <AddInstance root={root} open={adding} onOpenChange={setAdding} onAdded={(id) => select(id)} />
      <GroupName
        naming={naming}
        onClose={() => setNaming(null)}
        onSave={(name) => {
          if (naming?.mode === "new") void setGroup(naming.id, name);
          else if (naming?.mode === "rename") void renameGroup(naming.from, name);
          setNaming(null);
        }}
      />
    </main>
  );
}

// FrameInfo tells the open instance's chat what its bar shows, whenever
// that changes.
function FrameInfo({frame, instance, root}: {frame: React.RefObject<HTMLIFrameElement | null>; instance: HubInstance; root: string}) {
  const info = JSON.stringify(
    toFrame({
      name: displayName(instance),
      state: instanceState(instance),
      label: stateLabel(instance),
      agentType: instance.agent_type || undefined,
      agent: agentName(instance) || undefined,
      hostname: instance.hostname || undefined,
      tab: chatURL(basePath, root, instance.id),
    }),
  );
  useEffect(() => {
    frame.current?.contentWindow?.postMessage(JSON.parse(info), window.location.origin);
  }, [frame, info]);
  return null;
}

// InstanceBar heads the open instance: back to the list or overview, what
// it is and does, and its own chat in a tab of its own.
function InstanceBar({instance, root, onBack}: {instance: HubInstance; root: string; onBack: () => void}) {
  const state = instanceState(instance);
  const agent = agentName(instance);
  return (
    <div className="box-content flex h-12 shrink-0 items-center gap-1 border-b bg-background px-1.5 pt-[env(safe-area-inset-top)] sm:h-14 md:gap-2 md:px-3">
      <Button variant="ghost" size="icon" className="size-10 shrink-0 rounded-full md:hidden" onClick={onBack} aria-label="Back to Agents">
        <ArrowLeft className="size-5" />
      </Button>
      <Button variant="ghost" size="icon" className="hidden size-9 rounded-full md:inline-flex" onClick={onBack} aria-label="Back to the overview" title="Back to the overview (esc)">
        <X className="size-4" />
      </Button>
      <StatusGlyph state={state} className="ml-1 hidden sm:block" />
      <div className="min-w-0 flex-1 pl-0.5 md:pl-1">
        <p className="truncate text-sm leading-tight font-semibold">{displayName(instance)}</p>
        <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
          <span className={cn("shrink-0 font-medium", stateTone[state])}>{stateLabel(instance)}</span>
          {agent && (
            <>
              <span aria-hidden="true" className="text-muted-foreground/40">·</span>
              {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3" />}
              <span className="shrink-0">{agent}</span>
            </>
          )}
          {instance.hostname && (
            <>
              <span aria-hidden="true" className="hidden text-muted-foreground/40 sm:inline">·</span>
              <span className="hidden min-w-0 truncate sm:inline" data-hub-host>
                {instance.hostname}
              </span>
            </>
          )}
        </p>
      </div>
      <Button variant="ghost" size="sm" className="hidden h-9 shrink-0 gap-1.5 text-muted-foreground sm:inline-flex" asChild>
        <a href={chatURL(basePath, root, instance.id)} target="_blank" rel="noreferrer" aria-label={`Open ${displayName(instance)} in a new tab`}>
          <ExternalLink className="size-4" />
          <span className="hidden lg:inline">New tab</span>
        </a>
      </Button>
    </div>
  );
}
