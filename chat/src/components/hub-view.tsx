"use client";

import {useMemo, useRef, useState} from "react";
import {Button} from "@/components/ui/button";
import {chatURL, displayName, hubRoot, type HubInstance} from "@/lib/hub";
import type {DispatchJob} from "@/lib/hub-dispatch";
import {stoppedOf} from "@/lib/hub-filter";
import {needsYou} from "@/lib/hub-issues";
import {groupNames, groupOf} from "@/lib/hub-groups";
import {hostPrefix, machineOf} from "@/lib/hub-machines";
import {movePin, pinOf, sortPinned} from "@/lib/hub-order";
import {spendTotals} from "@/lib/hub-spend";
import {useStableCallback} from "@/lib/use-stable-callback";
import {cn} from "@/lib/utils";
import {HubMark} from "./hub/brand";
import {AddInstance, ForgetStopped, GroupName, type Naming} from "./hub/dialogs";
import {HubClock} from "./hub/clock";
import {HubList} from "./hub/hub-list";
import {IssuesView} from "./hub/issues/issues-view";
import {useIssuesRoute} from "./hub/issues/use-issues-route";
import {basePath, FrameFavorites, FrameInfo, InstanceBar} from "./hub/instance-bar";
import {HubListContext, type HubList as HubListShared, type RowActions} from "./hub/list-context";
import {ListEdge, useListWidth} from "./hub/list-edge";
import {HubListHeader} from "./hub/list-header";
import {machineAgents} from "./hub/machine-tabs";
import {useListView} from "./hub/list-order";
import {Overview} from "./hub/overview";
import {HubSettings, type SettingsSection} from "./hub/settings";
import {SignIn} from "./hub/sign-in";
import {useHubTab} from "./hub/tab-icon";
import {cancelFromHub} from "./hub/update";
import {useCollapsedGroups} from "./hub/use-collapsed-groups";
import {useHubActions} from "./hub/use-hub-actions";
import {useHubFrame} from "./hub/use-hub-frame";
import {agentDepth, useHubHistory} from "./hub/use-hub-history";
import {useHubKeys} from "./hub/use-hub-keys";
import {useHubStream} from "./hub/use-hub-stream";
import {useListFilters} from "./hub/use-list-filters";
import {useNeedsYou} from "./hub/use-needs-you";
import {useNotify} from "./hub/use-notify";

// HubView is the hub's page: sign in, the instances in a sidebar (what
// needs you first), and on the right either the overview or the chat of
// the one picked. On a narrow screen the list and the chat take turns.
export function HubView() {
  const root = useMemo(() => (typeof window === "undefined" ? "" : hubRoot(basePath, window.location.origin)), []);
  const {session, signedIn, retrySession, instances, hubVersion, favorites, setFavorites, dispatch, unreachable, status} = useHubStream(root);
  const {selected, select} = useHubHistory();
  const [adding, setAdding] = useState(false);
  // The Issues view, beside the list where the overview is.
  const issues = useIssuesRoute();
  // The issue open in its detail, which a list row's chip opens too.
  const [issueKey, setIssueKey] = useState<string | null>(null);
  // The jobs by the ID of each AgentAPI that worked on one, for the rows'
  // chips; and how many issues need you, for the Issues button.
  const jobsByInstance = useMemo(() => {
    const byID = new Map<string, DispatchJob>();
    for (const job of dispatch?.jobs ?? []) {
      for (const run of job.runs ?? []) byID.set(run.instance_id, job);
      if (job.instance_id) byID.set(job.instance_id, job);
    }
    return byID;
  }, [dispatch]);
  const issuesNeedYou = useMemo(() => (dispatch ? needsYou(dispatch.jobs, instances) : 0), [dispatch, instances]);
  const [naming, setNaming] = useState<Naming | null>(null);
  const [settings, setSettings] = useState<SettingsSection | null>(null);
  // The stopped agents the dialog asks to forget.
  const [forgetting, setForgetting] = useState<HubInstance[] | null>(null);
  const search = useRef<HTMLInputElement>(null);
  const listWidth = useListWidth();
  const view = useListView();
  const {notify, toggleNotify} = useNotify();
  const collapsed = useCollapsedGroups();

  const all = useMemo(() => [...instances.values()], [instances]);
  const filters = useListFilters(all, view);
  const {counts, sections} = filters;
  const spend = useMemo(() => spendTotals(all), [all]);
  const stopped = useMemo(() => stoppedOf(all), [all]);
  // The same list while the names are the same, so the rows don't render
  // again for it.
  const nameList = groupNames(all).join("\n");
  const names = useMemo(() => (nameList ? nameList.split("\n") : []), [nameList]);
  // The start most machines' names share, which their cards dim.
  const prefix = useMemo(() => hostPrefix(all.map(machineOf)), [all]);
  // The pinned rows the list shows, which Move up and Move down step past.
  const shownPins = sections[0]?.pinned ? sections[0].instances.map((instance) => instance.id) : [];

  // The agent open beside the list, and the agents of its machine, for the
  // bar's switch.
  const current = selected ? instances.get(selected) : undefined;
  const machine = useMemo(() => (current ? machineAgents(all, current) : []), [all, current]);
  const {frame, framed} = useHubFrame({
    onBack: () => select(undefined),
    onCancelUpdate: (id) => void cancelFromHub(root, id),
    // Another agent of the open one's machine, from the bar's switch.
    onOpen: (id) => {
      if (instances.has(id)) select(id);
    },
    // From the chat's menu of favorites.
    onManageFavorites: () => setSettings("favorites"),
  });

  // Tell people who aren't looking: a screen reader, the tab, a notification.
  const announcement = useNeedsYou(all, notify, select);
  useHubTab(counts["needs-you"], counts.working > 0);

  const actions = useHubActions(root, instances);
  useHubKeys({
    search,
    query: filters.query,
    onClearSearch: () => filters.setQuery(""),
    // Esc steps back: from an agent opened from Issues to Issues, from
    // Issues to the overview.
    onOverview: () => (selected ? select(undefined) : issues.show(false)),
    onIssues: () => {
      if (!issues.open || selected) openIssues();
    },
    onPin: (id) => {
      const instance = actions.instance(id);
      if (instance) void actions.setPinned(id, pinOf(instance) === undefined);
    },
  });

  // The list header's Issues: opens the view, or closes it; from an agent,
  // shows the view (the one it was opened from, or a new one).
  const openIssues = () => {
    if (!selected) return issues.show(!issues.open);
    if (issues.open || agentDepth(history.state) === 0) {
      select(undefined);
      if (!issues.open) issues.show(true);
      return;
    }
    // Back past the agents first, then the view's page on top.
    window.addEventListener("popstate", () => issues.show(true), {once: true});
    select(undefined);
  };

  // A row's chip opens its issue in Issues.
  const openIssue = useStableCallback((key: string) => {
    setIssueKey(key);
    if (!issues.open || selected) openIssues();
  });

  // Forget closes the agent once the hub forgot it.
  const forget = useStableCallback(async (id: string) => {
    if ((await actions.forget(id)) && selected === id) select(undefined);
  });
  // Forget all stopped closes the agent too, when the hub forgot it.
  const forgetStopped = async (ids: string[]) => {
    const forgot = await actions.forgetStopped(ids);
    if (selected && forgot.includes(selected)) select(undefined);
    setForgetting(null);
  };
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
      openIssue,
    }),
    [select, forget, actions, movePinned, openIssue],
  );
  const list = useMemo<HubListShared>(
    () => ({hubVersion, root, groups: names, prefix, jobs: jobsByInstance, actions: rowActions}),
    [hubVersion, root, names, prefix, jobsByInstance, rowActions],
  );
  const renameGroup = async (from: string, to: string) => {
    // A folded group stays folded under its new name.
    collapsed.rename(from, to);
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

  return (
    <HubClock fast={counts.working > 0}>
      <main className="app-frame flex overflow-hidden bg-background">
        <aside
          id="hub-list"
          style={{"--hub-list-width": `${listWidth.width}px`} as React.CSSProperties}
          className={cn("relative flex w-full flex-col border-r bg-background md:w-(--hub-list-width) md:shrink-0", current || issues.open ? "hidden md:flex" : "flex")}
        >
          <HubListHeader
            status={status}
            unreachable={unreachable}
            hubVersion={hubVersion}
            spend={spend}
            notify={notify}
            onToggleNotify={() => void toggleNotify()}
            onSettings={() => setSettings("favorites")}
            onAdd={() => setAdding(true)}
            issuesOpen={issues.open}
            issuesNeedYou={issuesNeedYou}
            onIssues={openIssues}
            search={search}
            filters={filters}
            view={view}
          />
          <HubListContext.Provider value={list}>
            <HubList
              root={root}
              filters={filters}
              order={view.order}
              collapsed={collapsed.collapsed}
              onToggle={collapsed.toggle}
              onRename={(from) => setNaming({mode: "rename", from})}
              selected={selected}
              onAdd={() => setAdding(true)}
              onForgetStopped={setForgetting}
            />
          </HubListContext.Provider>
          <p className="hidden border-t px-4 py-2 text-[11px] text-muted-foreground md:block" aria-hidden="true">
            <kbd className="font-mono">/</kbd> search · <kbd className="font-mono">i</kbd> issues · <kbd className="font-mono">j</kbd> <kbd className="font-mono">k</kbd> move ·{" "}
            {view.byMachine && (
              <>
                <kbd className="font-mono">←</kbd> <kbd className="font-mono">→</kbd> agent ·{" "}
              </>
            )}
            <kbd className="font-mono">↵</kbd> open · <kbd className="font-mono">p</kbd> pin · <kbd className="font-mono">esc</kbd> overview
          </p>
          <ListEdge controls="hub-list" {...listWidth} />
        </aside>

        <section className={cn("min-w-0 flex-1 flex-col bg-[color-mix(in_oklch,var(--muted)_45%,var(--background))]", current || issues.open ? "flex" : "hidden md:flex")}>
          {current ? (
            <>
              {framed?.instance === current.id ? (
                <>
                  <FrameInfo frame={frame} ready={framed.ready} instance={current} machine={machine} hubVersion={hubVersion} root={root} />
                  {favorites && <FrameFavorites frame={frame} ready={framed.ready} favorites={favorites} />}
                </>
              ) : (
                <InstanceBar instance={current} machine={machine} hubVersion={hubVersion} root={root} onBack={() => select(undefined)} onOpen={select} />
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
          ) : issues.open ? null : (
            <Overview
              root={root}
              instances={all}
              hubVersion={hubVersion}
              counts={counts}
              stopped={stopped.length}
              onOpen={select}
              onFilter={filters.setFilter}
              onAdd={() => setAdding(true)}
              onForgetStopped={() => setForgetting(stopped)}
            />
          )}
          {/* Kept under an agent opened from it, so Back finds it as it was. */}
          {issues.open && (
            <div className={cn("min-h-0 flex-1", current && "hidden")}>
              <IssuesView
                root={root}
                instances={instances}
                live={dispatch}
                selectedKey={issueKey}
                onSelect={setIssueKey}
                onClose={() => issues.show(false)}
                onOpenChat={select}
              />
            </div>
          )}
        </section>

        {/* Assertive: an agent that needs you. role="status" would make it polite. */}
        <div className="sr-only" aria-live="assertive" aria-atomic="true">
          {announcement}
        </div>
        <AddInstance root={root} open={adding} onOpenChange={setAdding} onAdded={(id) => select(id)} />
        <HubSettings root={root} section={settings} onClose={() => setSettings(null)} favorites={favorites} onFavoritesSaved={setFavorites} />
        <ForgetStopped instances={forgetting} onClose={() => setForgetting(null)} onForget={forgetStopped} />
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
