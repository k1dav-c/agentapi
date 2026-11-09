"use client";

import {useCallback, useEffect, useMemo, useRef, useState} from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import {ArrowLeft, Plus, RefreshCw, TriangleAlert} from "lucide-react";
import {Button} from "@/components/ui/button";
import type {DispatchBoard, HubInstance} from "@/lib/hub";
import {jobCost, type DispatchHost} from "@/lib/hub-dispatch";
import {boardCards, bySection, headline, sections, stageCounts, suggest, whyNoOption, type Card, type Option, type SectionID} from "@/lib/hub-issues";
import {useMediaQuery} from "@/lib/use-media-query";
import {cn} from "@/lib/utils";
import {Constellation} from "../brand";
import {IssueCard, type CardActions} from "./issue-card";
import {IssueDetail, type DetailActions} from "./issue-detail";
import {lookOf, type Look} from "./job-glyph";
import {MachinesStrip} from "./machines-strip";
import {Money} from "./money";
import {NewIssueDialog} from "./new-issue-dialog";
import {PipelineRail} from "./pipeline-rail";
import {ScopeMenu} from "./scope-menu";
import {ConnectGitHub, InstallApp, NoLaunchers, StartByRef} from "./setup";
import {recentRepos, useDispatch} from "./use-dispatch";
import {useFlip} from "./use-flip";

// How many done issues show before Show all.
const doneShown = 3;
const flashFor = 1600;

const heading = "flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase";

const reducedMotion = () => typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

// useFlashes are the cards whose look changed since the last read, for a
// moment: the ring they flash in their new state's color says what moved.
function useFlashes(cards: Card[]): ReadonlySet<string> {
  const looks = useRef<Map<string, Look> | null>(null);
  const [flashing, setFlashing] = useState<ReadonlySet<string>>(new Set());
  useEffect(() => {
    const next = new Map(cards.map((card) => [card.key, lookOf(card)]));
    const before = looks.current;
    looks.current = next;
    if (!before) return;
    const changed = [...next].filter(([key, look]) => before.has(key) && before.get(key) !== look).map(([key]) => key);
    if (changed.length === 0) return;
    setFlashing((keys) => new Set([...keys, ...changed]));
    const timer = setTimeout(() => setFlashing((keys) => new Set([...keys].filter((key) => !changed.includes(key)))), flashFor);
    return () => clearTimeout(timer);
  }, [cards]);
  return flashing;
}

function Skeleton({rows = 3}: {rows?: number}) {
  return (
    <ul className="space-y-2.5" aria-hidden="true">
      {Array.from({length: rows}, (_, i) => (
        <li key={i} className="flex gap-3 rounded-2xl border bg-card px-4 py-4" style={{opacity: 1 - i * 0.22}}>
          <span className="size-5 animate-pulse rounded-full bg-muted" />
          <span className="flex-1 space-y-2">
            <span className="block h-2.5 w-24 animate-pulse rounded bg-muted" />
            <span className="block h-3.5 w-3/4 animate-pulse rounded bg-muted" />
            <span className="block h-2.5 w-40 animate-pulse rounded bg-muted" />
          </span>
        </li>
      ))}
    </ul>
  );
}

// IssuesView is the hub's issue dispatch as a page of its own, beside the
// list where the overview is: the board of a repository's issues (or all
// of them) by stage, what needs you first; one tap assigns an issue to the
// agent and machine it suggests; an issue opens into its detail, a panel
// beside the board on a wide screen and a sheet over it on a narrow one.
export function IssuesView({
  root,
  instances,
  live,
  selectedKey,
  onSelect,
  onClose,
  onOpenChat,
}: {
  root: string;
  instances: ReadonlyMap<string, HubInstance>;
  // live is the machines and jobs from the hub's stream; undefined from a
  // hub before 3.12, which the view reads them from itself.
  live?: DispatchBoard;
  // The issue open in the detail (owner/repo#n), which a list row's chip
  // opens too.
  selectedKey: string | null;
  onSelect: (key: string | null) => void;
  onClose: () => void;
  onOpenChat: (instanceID: string) => void;
}) {
  const d = useDispatch(root, live);
  const hosts: DispatchHost[] = useMemo(() => (d.hosts.state === "ready" ? d.hosts.value : []), [d.hosts]);
  const status = d.github.state === "ready" ? d.github.value : undefined;
  const connected = !!status?.connected;
  const repoNames = useMemo(() => d.repos.map((repo) => repo.full_name), [d.repos]);
  const cards = useMemo(() => boardCards(d.issues, d.jobs, d.scope, instances), [d.issues, d.jobs, d.scope, instances]);
  const grouped = useMemo(() => bySection(cards), [cards]);
  const counts = useMemo(() => stageCounts(cards), [cards]);
  const flashing = useFlashes(cards);
  const setSelectedKey = onSelect;
  // A job out of the board's scope, opened from a list row, opens too.
  const selected = useMemo(
    () => cards.find((card) => card.key === selectedKey) ?? boardCards(new Map(), d.jobs.filter((job) => job.key === selectedKey), "", instances)[0],
    [cards, selectedKey, d.jobs, instances],
  );
  const [allDone, setAllDone] = useState(false);
  const [newIssue, setNewIssue] = useState(false);
  const wide = useMediaQuery("(min-width: 1280px)");
  const board = useRef<HTMLDivElement>(null);
  const title = useRef<HTMLHeadingElement>(null);

  // The board's order, which the cards glide to when it changes.
  useFlip(board, cards.map((card) => `${card.key}:${card.stage}:${card.waiting}`).join(" ") + (selectedKey ? "|open" : ""));
  // The keys work from the moment it opens.
  useEffect(() => title.current?.focus({preventScroll: true}), []);
  // An issue opened from a list row's chip comes into sight.
  useEffect(() => {
    if (!selectedKey) return;
    const card = board.current?.querySelector<HTMLElement>(`[data-flip="${CSS.escape(selectedKey)}"]`);
    card?.scrollIntoView({block: "nearest", behavior: reducedMotion() ? "auto" : "smooth"});
  }, [selectedKey]);

  // Who a tap on Assign gives an issue of each repository to.
  const suggestions = useMemo(() => {
    const out = new Map<string, Option | undefined>();
    for (const card of cards) if (!out.has(card.repo)) out.set(card.repo, suggest(hosts, card.repo, d.preferred));
    return out;
  }, [cards, hosts, d.preferred]);

  const {actions: dispatch, setPreferred} = d;
  const assignCard = useCallback(
    (card: Card, agent: string, host: string, picked: boolean) => {
      if (picked) setPreferred(agent);
      void dispatch.assign({repo: card.repo, number: card.number}, agent, host);
    },
    [dispatch, setPreferred],
  );
  const selectedRef = useRef(selectedKey);
  selectedRef.current = selectedKey;
  const latestDispatch = useRef(dispatch);
  latestDispatch.current = dispatch;
  const cardActions = useMemo<CardActions>(
    () => ({
      select: (key) => setSelectedKey(key === selectedRef.current ? null : key),
      assign: assignCard,
      retry: (card) => card.job && void latestDispatch.current.retry(card.job),
      openChat: onOpenChat,
    }),
    [assignCard, onOpenChat, setSelectedKey],
  );
  const detailActions: DetailActions = {...dispatch, openChat: onOpenChat, assignCard};

  const jump = (section: SectionID) => {
    if (section === "done") setAllDone(true);
    document.getElementById(`issues-${section}`)?.scrollIntoView({behavior: reducedMotion() ? "auto" : "smooth", block: "start"});
  };

  const closeDetail = () => {
    const key = selectedKey;
    setSelectedKey(null);
    // Back to the card it opened from.
    if (key) requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-issue-key="${CSS.escape(key)}"]`)?.focus());
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    const target = event.target as HTMLElement;
    if (event.key === "Escape") {
      if (target.closest("[role=menu], [role=dialog], [role=alertdialog]")) return;
      event.stopPropagation();
      if (selectedKey) closeDetail();
      else onClose();
      return;
    }
    if (event.metaKey || event.ctrlKey || event.altKey || target.closest("input, textarea, [contenteditable=true], [role=menu]")) return;
    const titles = [...(board.current?.querySelectorAll<HTMLElement>("[data-card-title]") ?? [])];
    const at = titles.indexOf(target);
    const focused = cards.find((card) => card.key === target.dataset.issueKey);
    switch (event.key) {
      case "j":
      case "ArrowDown":
      case "k":
      case "ArrowUp": {
        if (titles.length === 0) return;
        event.preventDefault();
        const step = event.key === "j" || event.key === "ArrowDown" ? 1 : -1;
        const next = titles[at === -1 ? (step > 0 ? 0 : titles.length - 1) : Math.min(titles.length - 1, Math.max(0, at + step))];
        next.focus();
        next.scrollIntoView({block: "nearest"});
        return;
      }
      case "a": {
        const pick = focused && !focused.job ? suggestions.get(focused.repo) : undefined;
        if (focused && pick) {
          event.preventDefault();
          assignCard(focused, pick.agent, pick.host.hostname, false);
        }
        return;
      }
      case "c": {
        const job = (focused ?? selected)?.job;
        if (job?.instance_id) onOpenChat(job.instance_id);
        return;
      }
      case "n":
        if (connected) {
          event.preventDefault();
          setNewIssue(true);
        }
        return;
      case "r":
        dispatch.refresh();
        return;
    }
  };

  const loading = d.github.state === "loading" || d.hosts.state === "loading";
  const spend = cards.reduce((sum, card) => sum + (card.job ? jobCost(card.job) : 0), 0);
  const sectionCounts = {attention: grouped.attention.length, working: grouped.working.length, todo: grouped.todo.length, done: grouped.done.length};
  // The rail's In progress: working, waiting on you, failed.
  const progress = counts.working + counts.failed;
  const sub = [
    progress > 0 && [`${progress} in progress`, counts.waiting > 0 && `${counts.waiting} ${counts.waiting === 1 ? "waits" : "wait"} on you`, counts.failed > 0 && `${counts.failed} failed`].filter(Boolean).join(", "),
    counts.review > 0 && `${counts.review} in review`,
    counts.todo > 0 && `${counts.todo} to do`,
  ]
    .filter(Boolean)
    .join(" · ");
  const noMachines = d.hosts.state === "ready" && hosts.length === 0;
  const firstRun = !loading && !connected && d.jobs.length === 0;
  const empty = !loading && connected && cards.length === 0 && !d.reading;

  const renderCard = (card: Card) => (
    <IssueCard
      key={card.key}
      card={card}
      hosts={hosts}
      instance={card.job ? instances.get(card.job.instance_id) : undefined}
      suggested={suggestions.get(card.repo)}
      disabledReason={whyNoOption(hosts, card.repo)}
      showRepo={d.scope === "" && repoNames.length !== 1}
      selected={card.key === selectedKey}
      busy={d.busy.has(card.key)}
      flash={flashing.has(card.key)}
      actions={cardActions}
    />
  );

  const detail = selected && (
    <IssueDetail
      card={selected}
      hosts={hosts}
      instance={selected.job ? instances.get(selected.job.instance_id) : undefined}
      busy={d.busy.has(selected.key)}
      suggested={suggestions.get(selected.repo) ?? suggest(hosts, selected.repo, d.preferred)}
      disabledReason={whyNoOption(hosts, selected.repo)}
      actions={detailActions}
      onClose={closeDetail}
      sheet={!wide}
    />
  );

  return (
    <div className="flex h-full min-h-0" onKeyDown={onKeyDown} data-issues-view>
      <div className="min-w-0 flex-1 overflow-y-auto" ref={board}>
        <header className="sticky top-0 z-20 border-b border-transparent bg-[color-mix(in_oklch,var(--muted)_45%,var(--background))]/85 px-5 pt-[max(0.75rem,env(safe-area-inset-top))] pb-3 backdrop-blur-md md:px-10">
          <div className="mx-auto flex max-w-3xl items-center gap-2">
            <Button variant="ghost" size="icon" className="-ml-2 size-9 shrink-0 rounded-full md:hidden" onClick={onClose} aria-label="Back to the list">
              <ArrowLeft className="size-4" />
            </Button>
            {connected && <ScopeMenu repos={repoNames} recent={recentRepos} jobs={d.jobs} scope={d.scope} onScope={d.setScope} />}
            <span className="flex-1" />
            <Button variant="ghost" size="icon" className="size-9 shrink-0 rounded-full text-muted-foreground" onClick={dispatch.refresh} aria-label="Read the issues again" title="Read again (r)">
              <RefreshCw className={cn("size-4", d.reading && "animate-spin")} />
            </Button>
            {connected && (
              <Button size="sm" className="h-9 shrink-0 rounded-full px-3.5" onClick={() => setNewIssue(true)} title="New issue (n)">
                <Plus className="size-4" /> <span className="max-sm:sr-only">New issue</span>
              </Button>
            )}
          </div>
        </header>

        <div className="mx-auto max-w-3xl px-5 pt-5 pb-20 md:px-10 md:pt-8">
          <p className="text-sm text-muted-foreground">
            Issues
            {status?.connected && status.slug && (
              <>
                {" "}
                · <span className="font-mono text-[13px]">{status.slug}</span>
                {(status.installations ?? []).length > 0 && ` on ${(status.installations ?? []).map((i) => i.account).join(", ")}`}
              </>
            )}
          </p>
          <h2
            ref={title}
            tabIndex={-1}
            className={cn("mt-1.5 text-3xl font-semibold tracking-tight text-balance outline-none md:text-4xl", sectionCounts.attention > 0 && "text-state-needs")}
          >
            {loading ? "Issues" : firstRun ? "Hand issues to your agents" : headline(sectionCounts)}
          </h2>
          <p className="mt-2 min-h-[1.5em] text-[15px] text-muted-foreground">
            {loading ? (
              "Reading the board…"
            ) : firstRun ? (
              "Connect GitHub, and every open issue is one tap from an agent on one of your machines."
            ) : sub || spend > 0 ? (
              <>
                {sub}
                {spend > 0 && (
                  <>
                    {sub && " · "}
                    <Money usd={spend} /> spent
                  </>
                )}
              </>
            ) : (
              "Assign an issue and its agent shows up here."
            )}
          </p>

          {d.pollError && (
            <p role="status" className="mt-5 flex items-center gap-2 rounded-xl border border-state-fault/25 bg-state-fault/[0.06] px-3.5 py-2.5 text-sm">
              <TriangleAlert className="size-4 shrink-0 text-state-fault" aria-hidden="true" />
              <span className="min-w-0 flex-1">
                Can&apos;t read the machines and jobs <span className="text-muted-foreground">· showing the last read · retrying</span>
              </span>
            </p>
          )}
          {d.github.state === "failed" && (
            <div role="alert" className="mt-5 flex flex-wrap items-center gap-3 rounded-xl border border-state-fault/25 bg-state-fault/[0.06] px-3.5 py-2.5 text-sm">
              <TriangleAlert className="size-4 shrink-0 text-state-fault" aria-hidden="true" />
              <span className="min-w-0 flex-1">
                Couldn&apos;t read the hub&apos;s GitHub setup <span className="text-muted-foreground">· {d.github.error}</span>
              </span>
              <Button size="sm" variant="outline" onClick={() => void d.retryGitHub()}>
                Try again
              </Button>
            </div>
          )}

          {loading ? (
            <div className="mt-8 space-y-8">
              <div className="h-[92px] animate-pulse rounded-2xl border bg-card" />
              <Skeleton />
            </div>
          ) : firstRun ? (
            <div className="mt-8 space-y-4">
              <ConnectGitHub root={root} />
              {noMachines ? <NoLaunchers /> : <StartByRef hosts={hosts} busy={d.busy.size > 0} onStart={(repo, issue, agent) => dispatch.assign({repo, number: issue}, agent, "")} />}
            </div>
          ) : (
            <>
              <div className="mt-7 space-y-3">
                <PipelineRail counts={counts} onJump={jump} />
                {hosts.length > 0 && <MachinesStrip hosts={hosts} />}
              </div>
              {status?.connected && (status.installations ?? []).length === 0 && (
                <div className="mt-5">
                  <InstallApp status={status} />
                </div>
              )}
              {noMachines && (
                <div className="mt-5">
                  <NoLaunchers compact />
                </div>
              )}
              {!connected && (
                <div className="mt-5 space-y-3">
                  <StartByRef hosts={hosts} busy={d.busy.size > 0} onStart={(repo, issue, agent) => dispatch.assign({repo, number: issue}, agent, "")} />
                </div>
              )}

              {empty ? (
                <div className="hub-rise mt-14 flex flex-col items-center text-center">
                  <Constellation className="w-52 opacity-80" />
                  <p className="mt-6 text-base font-medium">No open issues in {d.scope || "your repositories"}</p>
                  <p className="mt-1 text-sm text-muted-foreground">Write one, and hand it to an agent as it opens.</p>
                  <Button className="mt-5 rounded-full" onClick={() => setNewIssue(true)}>
                    <Plus className="size-4" /> New issue
                  </Button>
                </div>
              ) : (
                sections.map(({id, title: sectionTitle}) => {
                  const list = grouped[id];
                  const reading = id === "todo" && d.reading && list.length === 0;
                  if (list.length === 0 && !reading && !(id === "todo" && d.issuesError)) return null;
                  const shown = id === "done" && !allDone ? list.slice(0, doneShown) : list;
                  return (
                    <section key={id} id={`issues-${id}`} aria-labelledby={`issues-${id}-title`} className="mt-10 scroll-mt-20">
                      <h3 id={`issues-${id}-title`} data-flip={`section ${id}`} className={cn(heading, id === "attention" && "text-state-needs")}>
                        {sectionTitle}
                        <span className="tabular-nums opacity-60">{list.length}</span>
                      </h3>
                      {id === "todo" && d.issuesError && (
                        <p role="alert" className="mt-3 flex items-center gap-2 text-sm text-state-fault">
                          <TriangleAlert className="size-4 shrink-0" aria-hidden="true" />
                          <span className="min-w-0 flex-1 truncate" title={d.issuesError}>
                            Couldn&apos;t read the issues of {d.issuesError.split("\n").map((line) => line.split(":")[0]).join(", ")}
                          </span>
                          <button type="button" className="shrink-0 font-medium underline underline-offset-4" onClick={dispatch.refresh}>
                            Try again
                          </button>
                        </p>
                      )}
                      <div className="mt-3">
                        {reading ? (
                          <Skeleton rows={2} />
                        ) : (
                          <ul className="space-y-2.5" aria-label={sectionTitle}>
                            {shown.map(renderCard)}
                          </ul>
                        )}
                      </div>
                      {id === "done" && list.length > doneShown && (
                        <button
                          type="button"
                          data-flip="show all"
                          onClick={() => setAllDone(!allDone)}
                          className="mt-3 text-sm font-medium text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                        >
                          {allDone ? "Show fewer" : `Show all ${list.length}`}
                        </button>
                      )}
                    </section>
                  );
                })
              )}
            </>
          )}
          <p className={cn("mt-14 hidden text-[11px] text-muted-foreground", !firstRun && !loading && "md:block")} aria-hidden="true">
            <kbd className="font-mono">j</kbd> <kbd className="font-mono">k</kbd> move · <kbd className="font-mono">↵</kbd> open · <kbd className="font-mono">a</kbd> assign ·{" "}
            <kbd className="font-mono">c</kbd> chat · <kbd className="font-mono">n</kbd> new issue · <kbd className="font-mono">r</kbd> read again · <kbd className="font-mono">esc</kbd> back
          </p>
        </div>
      </div>

      {wide ? (
        selected && (
          <aside
            key="detail"
            aria-label={`#${selected.number} ${selected.title}`}
            className="issue-panel w-[440px] shrink-0 border-l bg-background 2xl:w-[500px]"
          >
            {detail}
          </aside>
        )
      ) : (
        <DialogPrimitive.Root open={!!selected} onOpenChange={(open) => !open && closeDetail()}>
          <DialogPrimitive.Portal>
            <DialogPrimitive.Overlay className="fixed inset-0 z-40 hidden bg-black/30 backdrop-blur-[2px] data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0 md:block" />
            <DialogPrimitive.Content
              aria-describedby={undefined}
              className="app-frame z-50 bg-background shadow-2xl duration-300 data-[state=closed]:animate-out data-[state=closed]:slide-out-to-right data-[state=open]:animate-in data-[state=open]:slide-in-from-right md:left-auto md:w-[480px] md:border-l"
            >
              <DialogPrimitive.Title className="sr-only">{selected ? `#${selected.number} ${selected.title}` : "Issue"}</DialogPrimitive.Title>
              {detail}
            </DialogPrimitive.Content>
          </DialogPrimitive.Portal>
        </DialogPrimitive.Root>
      )}

      {connected && (
        <NewIssueDialog
          open={newIssue}
          onOpenChange={setNewIssue}
          repos={repoNames}
          initialRepo={d.scope}
          hosts={hosts}
          preferred={d.preferred}
          busy={[...d.busy].some((key) => key.startsWith("new "))}
          onCreate={async (request) => (await dispatch.createIssue(request)) !== undefined}
        />
      )}
    </div>
  );
}
