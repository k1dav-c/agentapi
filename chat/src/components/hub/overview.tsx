"use client";

import {Fragment} from "react";
import {ArrowRight, CircleCheck, ClipboardList, Gauge, KeyRound, MessageCircleQuestion, ShieldQuestion, SquareTerminal} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {
  activity,
  countedSpends,
  countedTokens,
  displayName,
  formatAgo,
  formatElapsed,
  formatTokens,
  formatUSD,
  formatWork,
  instanceState,
  instanceVersion,
  parseVersion,
  spendTitle,
  spendTotals,
  tokenKinds,
  tokensTitle,
  tokenTotals,
  versionTotals,
  workSeconds,
  workTitle,
  workTotals,
  type HubFilter,
  type HubInstance,
  type InstanceState,
  type VersionRelation,
} from "@/lib/hub";
import {olderUpdates} from "@/lib/hub-update";
import {cn} from "@/lib/utils";
import {useHubTime} from "./clock";
import {Constellation} from "./brand";
import {StatusGlyph} from "./status-glyph";
import {agentName} from "./instance-row";
import {JoinMachine} from "./join-machine";
import {UpdateOlder} from "./update";
import {OlderMark, VersionTag} from "./version-tag";

// What a waiting instance asks for: its icon and the word for it.
function ask(instance: HubInstance): {icon: typeof ShieldQuestion; label: string} {
  const summary = instance.summary;
  switch (summary?.attention) {
    case "login":
      return {icon: KeyRound, label: "Login"};
    case "limit":
      return {icon: Gauge, label: "Usage limit"};
    case "dialog":
      return {icon: SquareTerminal, label: "Terminal dialog"};
  }
  switch (summary?.request?.kind) {
    case "question":
      return {icon: MessageCircleQuestion, label: "Question"};
    case "plan":
      return {icon: ClipboardList, label: "Plan"};
    default:
      return {icon: ShieldQuestion, label: "Approval"};
  }
}

// What the card's button says: a request is answered, anything else is
// looked at.
function action(instance: HubInstance): string {
  return instance.summary?.attention === "request" || instance.summary?.attention === "dialog" ? "Answer" : "Open";
}

function greeting(now: number): string {
  const hour = new Date(now).getHours();
  return hour < 5 ? "Working late" : hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
}

// Greeting greets by the time of day, and with date, says the day; it
// renders again when that changes.
function Greeting({date = false}: {date?: boolean}) {
  return useHubTime((now) =>
    date ? `${greeting(now)} · ${new Date(now).toLocaleDateString(undefined, {weekday: "long", month: "long", day: "numeric"})}` : greeting(now),
  );
}

function Meta({instance, hubVersion}: {instance: HubInstance; hubVersion: string}) {
  const agent = agentName(instance);
  const version = instanceVersion(instance);
  return (
    <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
      {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3" />}
      <span className="shrink-0">{agent}</span>
      {instance.hostname && (
        <>
          <span aria-hidden="true" className="text-muted-foreground/40">·</span>
          <span className="min-w-0 truncate" data-hub-host>
            {instance.hostname}
          </span>
        </>
      )}
      {version && (
        <>
          <span aria-hidden="true" className="text-muted-foreground/40">·</span>
          <VersionTag version={version} hubVersion={hubVersion} lastKnown={instance.connection !== "connected"} />
        </>
      )}
    </span>
  );
}

function Section({title, count, children}: {title: string; count: number; children: React.ReactNode}) {
  return (
    <section className="mt-10">
      <h3 className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
        {title}
        <span className="text-muted-foreground/60 tabular-nums">{count}</span>
      </h3>
      <div className="mt-3 grid gap-3 md:grid-cols-2 2xl:grid-cols-3">{children}</div>
    </section>
  );
}

const cardClass =
  "group/card hub-rise flex min-w-0 flex-col rounded-2xl border bg-card p-4 text-left shadow-xs outline-none transition-[box-shadow,transform,border-color] hover:-translate-y-0.5 hover:shadow-md focus-visible:ring-[3px] focus-visible:ring-ring/50";

function NeedsCard({instance, hubVersion, onOpen}: {instance: HubInstance; hubVersion: string; onOpen: () => void}) {
  const {icon: Icon, label} = ask(instance);
  const since = activity(instance).since;
  const ago = useHubTime((now) => formatAgo(since, now));
  return (
    <button type="button" onClick={onOpen} className={cn(cardClass, "border-state-needs/35 hover:border-state-needs/60")}>
      <span className="flex items-center gap-2">
        <span className="flex size-7 items-center justify-center rounded-lg bg-state-needs/12 text-state-needs">
          <Icon className="size-4" aria-hidden="true" />
        </span>
        <span className="text-xs font-semibold tracking-wide text-state-needs uppercase">{label}</span>
        {since && (
          <span className="ml-auto text-xs text-muted-foreground tabular-nums">
            {ago === "now" ? "just now" : `waiting ${ago}`}
          </span>
        )}
      </span>
      <span className="mt-3 line-clamp-2 text-[15px] leading-snug font-medium text-pretty">{activity(instance).text}</span>
      <span className="mt-auto flex items-end justify-between gap-3 pt-4">
        <span className="min-w-0">
          <span className="block truncate text-sm font-medium">{displayName(instance)}</span>
          <Meta instance={instance} hubVersion={hubVersion} />
        </span>
        <span className="flex shrink-0 items-center gap-1 rounded-lg bg-foreground px-3 py-1.5 text-sm font-medium text-background transition-transform group-hover/card:translate-x-0.5">
          {action(instance)} <ArrowRight className="size-3.5" aria-hidden="true" />
        </span>
      </span>
    </button>
  );
}

function WorkingCard({instance, hubVersion, onOpen}: {instance: HubInstance; hubVersion: string; onOpen: () => void}) {
  const doing = activity(instance);
  const elapsed = useHubTime((now) => formatElapsed(doing.since, now));
  return (
    <button type="button" onClick={onOpen} className={cardClass}>
      <span className="flex items-baseline justify-between gap-3">
        <span className="min-w-0 truncate text-sm font-medium">{displayName(instance)}</span>
        <span className="shrink-0 font-mono text-sm text-state-working tabular-nums">{elapsed}</span>
      </span>
      <span className="mt-1.5 line-clamp-2 text-sm text-muted-foreground">{doing.text}</span>
      <span className="hub-shimmer mt-4 block h-1 rounded-full bg-state-working/12" aria-hidden="true" />
      <span className="mt-3">
        <Meta instance={instance} hubVersion={hubVersion} />
      </span>
    </button>
  );
}

function FinishedCard({instance, hubVersion, onOpen}: {instance: HubInstance; hubVersion: string; onOpen: () => void}) {
  const doing = activity(instance);
  const ago = useHubTime((now) => formatAgo(doing.since, now));
  return (
    <button type="button" onClick={onOpen} className={cardClass}>
      <span className="flex items-center gap-2">
        <CircleCheck className="size-4 text-state-ready" aria-hidden="true" />
        <span className="min-w-0 truncate text-sm font-medium">{displayName(instance)}</span>
        <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">{ago}</span>
      </span>
      <span className="mt-2 line-clamp-2 text-sm text-muted-foreground">{doing.text}</span>
      <span className="mt-3">
        <Meta instance={instance} hubVersion={hubVersion} />
      </span>
    </button>
  );
}

const stats: {filter: HubFilter; label: string; tone: string; tint: string; glyph: InstanceState}[] = [
  {filter: "needs-you", label: "Need you", tone: "text-state-needs", tint: "bg-state-needs/6 border-state-needs/30", glyph: "needs-you"},
  {filter: "working", label: "Working", tone: "text-state-working", tint: "bg-state-working/5 border-state-working/25", glyph: "working"},
  {filter: "unread", label: "Finished, unseen", tone: "text-state-ready", tint: "bg-state-ready/5 border-state-ready/25", glyph: "unread"},
  {filter: "offline", label: "Offline", tone: "text-state-fault", tint: "bg-state-fault/5 border-state-fault/25", glyph: "unreachable"},
];

// SpendCard is what the instances spent in the last 24 hours and 7 days,
// all of them and the ones that spent the most this week.
function SpendCard({instances, onOpen}: {instances: HubInstance[]; onOpen: (id: string) => void}) {
  const totals = spendTotals(instances);
  if (totals.week <= 0) return null;
  // Each scope once, as the totals count them.
  const top = countedSpends(instances)
    .filter(({spend}) => spend.week_usd > 0)
    .sort((a, b) => b.spend.week_usd - a.spend.week_usd)
    .slice(0, 5);
  return (
    <section className="mt-10" aria-labelledby="hub-spend">
      <h3 id="hub-spend" className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
        Spend
        <span className="font-normal tracking-normal normal-case text-muted-foreground/70">API-equivalent, at list prices; a subscription doesn&apos;t charge it</span>
      </h3>
      <div className="mt-3 grid gap-6 rounded-2xl border bg-card p-5 shadow-xs md:grid-cols-[auto_minmax(0,1fr)]">
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3 md:flex-col md:items-start md:border-r md:pr-8">
          <dl className="flex gap-8 md:flex-col md:gap-4">
            <div>
              <dt className="text-xs text-muted-foreground">Last 24 hours</dt>
              <dd className="text-3xl font-semibold tracking-tight tabular-nums">{formatUSD(totals.day)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Last 7 days</dt>
              <dd className="text-3xl font-semibold tracking-tight tabular-nums">{formatUSD(totals.week)}</dd>
            </div>
          </dl>
          <p className="text-xs text-muted-foreground">
            {totals.counted} of {instances.length} agents report it
          </p>
        </div>
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-muted-foreground">
              <th className="pb-2 font-normal">Spent the most this week</th>
              <th className="pb-2 text-right font-normal">24 hours</th>
              <th className="pb-2 text-right font-normal">7 days</th>
            </tr>
          </thead>
          <tbody>
            {top.map(({instance, spend}) => (
              <tr key={instance.id} className="border-t" title={spendTitle(spend.day_usd, spend.week_usd)}>
                <td className="max-w-0 py-2 pr-3">
                  <button
                    type="button"
                    onClick={() => onOpen(instance.id)}
                    className="flex w-full min-w-0 items-center gap-2 rounded text-left outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
                  >
                    {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3.5 shrink-0" />}
                    <span className="truncate font-medium">{displayName(instance)}</span>
                  </button>
                </td>
                <td className="py-2 text-right tabular-nums">{formatUSD(spend.day_usd)}</td>
                <td className="py-2 pl-4 text-right tabular-nums">{formatUSD(spend.week_usd)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// TokensCard is the tokens the instances used in the last 24 hours and 7
// days, the calls their spend adds up: all of them by kind, the ones that
// used the most this week, and each group.
export function TokensCard({instances, onOpen}: {instances: HubInstance[]; onOpen: (id: string) => void}) {
  const totals = tokenTotals(instances);
  if (totals.week.total <= 0) return null;
  // Each scope once, as the totals count them.
  const counted = countedTokens(instances);
  const top = counted
    .filter(({week}) => week.total > 0)
    .sort((a, b) => b.week.total - a.week.total)
    .slice(0, 5);
  const byGroup = new Map<string, {day: number; week: number}>();
  for (const {instance, day, week} of counted) {
    const name = instance.group?.trim() ?? "";
    const group = byGroup.get(name) ?? {day: 0, week: 0};
    group.day += day.total;
    group.week += week.total;
    byGroup.set(name, group);
  }
  // Subtotals only when some agents are in a group, named ones first.
  const groups =
    byGroup.size > 1 || !byGroup.has("")
      ? [...byGroup.entries()].filter(([, group]) => group.week > 0).sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)))
      : [];
  const header = (first: string) => (
    <thead>
      <tr className="text-left text-xs text-muted-foreground">
        <th className="pb-2 font-normal">{first}</th>
        <th className="pb-2 text-right font-normal">24 hours</th>
        <th className="pb-2 text-right font-normal">7 days</th>
      </tr>
    </thead>
  );
  return (
    <section className="mt-10" aria-labelledby="hub-tokens">
      <h3 id="hub-tokens" className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
        Tokens
        <span className="font-normal tracking-normal normal-case text-muted-foreground/70">The calls the spend adds up, models without a price included</span>
      </h3>
      <div className="mt-3 grid gap-6 rounded-2xl border bg-card p-5 shadow-xs md:grid-cols-[auto_minmax(0,1fr)]">
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3 md:flex-col md:items-start md:border-r md:pr-8">
          <dl className="flex gap-8 md:flex-col md:gap-4">
            <div title={`${totals.day.total.toLocaleString()} tokens`}>
              <dt className="text-xs text-muted-foreground">Last 24 hours</dt>
              <dd className="text-3xl font-semibold tracking-tight tabular-nums">{formatTokens(totals.day.total)}</dd>
            </div>
            <div title={`${totals.week.total.toLocaleString()} tokens`}>
              <dt className="text-xs text-muted-foreground">Last 7 days</dt>
              <dd className="text-3xl font-semibold tracking-tight tabular-nums">{formatTokens(totals.week.total)}</dd>
            </div>
          </dl>
          <p className="text-xs text-muted-foreground">
            {totals.counted} of {instances.length} agents report it
          </p>
        </div>
        <div className="min-w-0 space-y-5">
          <table className="w-full text-sm">
            {header("By kind")}
            <tbody>
              {tokenKinds.map(({key, label}) => (
                <tr key={key} className="border-t" title={tokensTitle(totals.day[key], totals.week[key])}>
                  <td className="py-2 pr-3">{label}</td>
                  <td className="py-2 text-right tabular-nums">{formatTokens(totals.day[key])}</td>
                  <td className="py-2 pl-4 text-right tabular-nums">{formatTokens(totals.week[key])}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <table className="w-full text-sm">
            {header("Used the most this week")}
            <tbody>
              {top.map(({instance, day, week}) => (
                <tr key={instance.id} className="border-t" title={tokensTitle(day.total, week.total)}>
                  <td className="max-w-0 py-2 pr-3">
                    <button
                      type="button"
                      onClick={() => onOpen(instance.id)}
                      className="flex w-full min-w-0 items-center gap-2 rounded text-left outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
                    >
                      {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3.5 shrink-0" />}
                      <span className="truncate font-medium">{displayName(instance)}</span>
                    </button>
                  </td>
                  <td className="py-2 text-right tabular-nums">{formatTokens(day.total)}</td>
                  <td className="py-2 pl-4 text-right tabular-nums">{formatTokens(week.total)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {groups.length > 0 && (
            <table className="w-full text-sm">
              {header("By group")}
              <tbody>
                {groups.map(([name, group]) => (
                  <tr key={name} className="border-t" title={tokensTitle(group.day, group.week)}>
                    <td className={cn("max-w-0 truncate py-2 pr-3", name ? "font-medium" : "text-muted-foreground")}>{name || "Ungrouped"}</td>
                    <td className="py-2 text-right tabular-nums">{formatTokens(group.day)}</td>
                    <td className="py-2 pl-4 text-right tabular-nums">{formatTokens(group.week)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </section>
  );
}

// The work time the card shows at a moment, its figures formatted: it
// renders again when one of them changes, not at every tick of the clock.
interface WorkView {
  day: string;
  week: string;
  counted: number;
  top: {id: string; day: string; week: string; title: string}[];
  groups: {name: string; day: string; week: string; title: string}[];
}

function workView(instances: HubInstance[], now: number): WorkView | undefined {
  const totals = workTotals(instances, now);
  if (totals.week <= 0) return undefined;
  const each = instances.map((instance) => ({instance, seconds: workSeconds(instance, now)}));
  const top = each
    .filter(({seconds}) => seconds.week > 0)
    .sort((a, b) => b.seconds.week - a.seconds.week)
    .slice(0, 5)
    .map(({instance, seconds}) => ({id: instance.id, day: formatWork(seconds.day), week: formatWork(seconds.week), title: workTitle(seconds.day, seconds.week)}));
  const byGroup = new Map<string, {day: number; week: number}>();
  for (const {instance, seconds} of each) {
    const name = instance.group?.trim() ?? "";
    const group = byGroup.get(name) ?? {day: 0, week: 0};
    group.day += seconds.day;
    group.week += seconds.week;
    byGroup.set(name, group);
  }
  // Subtotals only when some agents are in a group, named ones first.
  const groups = byGroup.size > 1 || !byGroup.has("")
    ? [...byGroup.entries()]
        .filter(([, group]) => group.week > 0)
        .sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)))
        .map(([name, group]) => ({name, day: formatWork(group.day), week: formatWork(group.week), title: workTitle(group.day, group.week)}))
    : [];
  return {day: formatWork(totals.day), week: formatWork(totals.week), counted: totals.counted, top, groups};
}

const sameView = (a: WorkView | undefined, b: WorkView | undefined) => JSON.stringify(a) === JSON.stringify(b);

// WorkCard is how long the agents worked in the last 24 hours and 7 days,
// as the hub saw it: all of them, each group, and the ones that worked the
// most this week. It reads the hub's clock for the spans going on.
export function WorkCard({instances, onOpen}: {instances: HubInstance[]; onOpen: (id: string) => void}) {
  const view = useHubTime((now) => workView(instances, now), sameView);
  if (!view) return null;
  const byID = new Map(instances.map((instance) => [instance.id, instance]));
  return (
    <section className="mt-10" aria-labelledby="hub-work">
      <h3 id="hub-work" className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
        Work time
        <span className="font-normal tracking-normal normal-case text-muted-foreground/70">How long the agents worked, as the hub saw it</span>
      </h3>
      <div className="mt-3 grid gap-6 rounded-2xl border bg-card p-5 shadow-xs md:grid-cols-[auto_minmax(0,1fr)]">
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3 md:flex-col md:items-start md:border-r md:pr-8">
          <dl className="flex gap-8 md:flex-col md:gap-4">
            <div>
              <dt className="text-xs text-muted-foreground">Last 24 hours</dt>
              <dd className="text-3xl font-semibold tracking-tight tabular-nums">{view.day}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Last 7 days</dt>
              <dd className="text-3xl font-semibold tracking-tight tabular-nums">{view.week}</dd>
            </div>
          </dl>
          <p className="text-xs text-muted-foreground">
            {view.counted} of {instances.length} agents worked this week
          </p>
        </div>
        <div className="min-w-0 space-y-5">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs text-muted-foreground">
                <th className="pb-2 font-normal">Worked the most this week</th>
                <th className="pb-2 text-right font-normal">24 hours</th>
                <th className="pb-2 text-right font-normal">7 days</th>
              </tr>
            </thead>
            <tbody>
              {view.top.map((row) => {
                const instance = byID.get(row.id);
                if (!instance) return null;
                return (
                  <tr key={row.id} className="border-t" title={row.title}>
                    <td className="max-w-0 py-2 pr-3">
                      <button
                        type="button"
                        onClick={() => onOpen(row.id)}
                        className="flex w-full min-w-0 items-center gap-2 rounded text-left outline-none hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
                      >
                        {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3.5 shrink-0" />}
                        <span className="truncate font-medium">{displayName(instance)}</span>
                      </button>
                    </td>
                    <td className="py-2 text-right tabular-nums">{row.day}</td>
                    <td className="py-2 pl-4 text-right tabular-nums">{row.week}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {view.groups.length > 0 && (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-muted-foreground">
                  <th className="pb-2 font-normal">By group</th>
                  <th className="pb-2 text-right font-normal">24 hours</th>
                  <th className="pb-2 text-right font-normal">7 days</th>
                </tr>
              </thead>
              <tbody>
                {view.groups.map((group) => (
                  <tr key={group.name} className="border-t" title={group.title}>
                    <td className={cn("max-w-0 truncate py-2 pr-3", group.name ? "font-medium" : "text-muted-foreground")}>{group.name || "Ungrouped"}</td>
                    <td className="py-2 text-right tabular-nums">{group.day}</td>
                    <td className="py-2 pl-4 text-right tabular-nums">{group.week}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </section>
  );
}

// What a version that isn't older is to the hub's, next to it in the
// Versions card.
const relationNote: Partial<Record<VersionRelation, string>> = {same: "the hub's", newer: "newer than the hub"};

// The first release whose AgentAPIs restart in place, which
// agentapi update --restart needs.
const RESTARTS_IN_PLACE = "v2.1.0";

// VersionsCard is the AgentAPI versions of the agents the hub follows: how
// many run each, on which machines, and how many run an older one than the
// hub, with a button that updates those it can and how to update the
// others.
export function VersionsCard({root, instances, hubVersion}: {root: string; instances: HubInstance[]; hubVersion: string}) {
  const totals = versionTotals(instances, hubVersion);
  if (totals.counted === 0) return null;
  const release = parseVersion(hubVersion) !== undefined;
  const older = olderUpdates(instances, hubVersion);
  const byHand = older.blocked.length + older.off.length + older.unsupported.length;
  return (
    <section className="mt-10" aria-labelledby="hub-versions">
      <h3 id="hub-versions" className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
        Versions
        <span className="font-normal tracking-normal normal-case text-muted-foreground/70">
          {release ? `The AgentAPI version of each agent; the hub runs v${hubVersion}` : "The hub is a development build, so no version counts as older"}
        </span>
      </h3>
      <div className={cn("mt-3 grid gap-6 rounded-2xl border bg-card p-5 shadow-xs", release && "md:grid-cols-[auto_minmax(0,1fr)]")}>
        {release && (
          <div className="flex flex-wrap items-end gap-x-8 gap-y-3 md:flex-col md:items-start md:border-r md:pr-8">
            <div>
              <p className="text-xs text-muted-foreground">Older than the hub</p>
              <p className={cn("text-3xl font-semibold tracking-tight tabular-nums", totals.older === 0 && "text-muted-foreground/50")}>{totals.older}</p>
            </div>
            <p className="text-xs text-muted-foreground">
              of {totals.counted} connected {totals.counted === 1 ? "agent" : "agents"}
            </p>
          </div>
        )}
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-muted-foreground">
              <th className="pb-2 font-normal">Version</th>
              <th className="pb-2 font-normal">Machines</th>
              <th className="pb-2 text-right font-normal">Agents</th>
            </tr>
          </thead>
          <tbody>
            {totals.versions.map((count) => (
              <tr key={count.version} className="border-t">
                <td className="py-2 pr-4 whitespace-nowrap">
                  <span className="font-medium tabular-nums">{count.version ? `v${count.version}` : "Not reported"}</span>
                  {count.relation === "older" && <OlderMark className="ml-2 text-xs" />}
                  {relationNote[count.relation] && <span className="ml-2 text-xs text-muted-foreground">{relationNote[count.relation]}</span>}
                </td>
                <td className="max-w-0 py-2 pr-4 text-xs text-muted-foreground">
                  <span className="block truncate" title={count.machines.join(", ")}>
                    {count.machines.map((name, i) => (
                      <Fragment key={name}>
                        {i > 0 && ", "}
                        <span data-hub-host>{name}</span>
                      </Fragment>
                    ))}
                  </span>
                </td>
                <td className="py-2 text-right tabular-nums">{count.instances}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {totals.older > 0 && (
          <div className="space-y-2 text-xs leading-5 text-muted-foreground md:col-span-2 md:border-t md:pt-4">
            <UpdateOlder root={root} older={older} />
            {byHand > 0 && (
              <p>
                To update: <code className="font-mono text-[12px] text-foreground">sudo agentapi update --restart</code> on machines with {RESTARTS_IN_PLACE} or later (no{" "}
                <code className="font-mono text-[12px] text-foreground">sudo</code> if the binary is yours); before {RESTARTS_IN_PLACE}, update, then restart each AgentAPI once.
              </p>
            )}
          </div>
        )}
      </div>
    </section>
  );
}

// Overview is the main pane while no instance is open: what needs you,
// what runs, and what finished since you last looked.
export function Overview({
  root,
  instances,
  hubVersion,
  counts,
  onOpen,
  onFilter,
  onAdd,
}: {
  root: string;
  instances: HubInstance[];
  hubVersion: string;
  counts: Record<HubFilter, number>;
  onOpen: (id: string) => void;
  onFilter: (filter: HubFilter) => void;
  onAdd: () => void;
}) {
  const by = (state: string) => instances.filter((instance) => instanceState(instance) === state);
  const needs = by("needs-you");
  const working = by("working").sort((a, b) => (a.summary?.task?.started_at ?? "").localeCompare(b.summary?.task?.started_at ?? ""));
  const unread = by("unread");
  if (instances.length === 0) {
    return (
      <div className="h-full overflow-y-auto">
        <div className="mx-auto max-w-2xl px-10 pt-12 pb-16">
          <p className="text-sm text-muted-foreground">
            <Greeting />
          </p>
          <h2 className="mt-2 text-4xl font-semibold tracking-tight">Connect your first agent</h2>
          <p className="mt-3 text-[15px] text-muted-foreground">Each machine joins once. Its agents show up here and stay after restarts.</p>
          <Onboarding root={root} onAdd={onAdd} />
        </div>
      </div>
    );
  }
  const headline =
    needs.length > 0
        ? `${needs.length} ${needs.length === 1 ? "agent needs" : "agents need"} you`
        : working.length > 0
          ? "Everything's running"
          : "All quiet";
  const sub = [
          working.length && `${working.length} working`,
          unread.length && `${unread.length} finished since you looked`,
          counts.offline && `${counts.offline} offline`,
    `${instances.length} in all`,
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-10 pt-12 pb-16">
        <p className="text-sm text-muted-foreground">
          <Greeting date />
        </p>
        <h2 className={cn("mt-2 text-4xl font-semibold tracking-tight text-balance", needs.length > 0 && "text-state-needs")}>{headline}</h2>
        <p className="mt-3 text-[15px] text-muted-foreground">{sub}</p>

        <div className="mt-8 grid grid-cols-4 gap-3">
          {stats.map((stat) => (
            <button
              key={stat.filter}
              type="button"
              onClick={() => onFilter(stat.filter)}
              aria-label={`${counts[stat.filter]} ${stat.label}: show them in the list`}
              className={cn(
                "flex items-start justify-between rounded-2xl border bg-card px-4 py-3.5 text-left shadow-xs outline-none transition-shadow hover:shadow-md focus-visible:ring-[3px] focus-visible:ring-ring/50",
                counts[stat.filter] > 0 && stat.tint,
              )}
            >
              <span>
                <span className={cn("block text-3xl font-semibold tracking-tight tabular-nums", counts[stat.filter] > 0 ? stat.tone : "text-muted-foreground/50")}>
                  {counts[stat.filter]}
                </span>
                <span className="mt-0.5 block text-xs text-muted-foreground">{stat.label}</span>
              </span>
              <StatusGlyph state={counts[stat.filter] > 0 ? stat.glyph : "idle"} className="mt-1 size-4" />
            </button>
          ))}
        </div>

        <SpendCard instances={instances} onOpen={onOpen} />
        <TokensCard instances={instances} onOpen={onOpen} />
        <WorkCard instances={instances} onOpen={onOpen} />

        {needs.length > 0 && (
          <Section title="Needs you" count={needs.length}>
            {needs.map((instance) => (
              <NeedsCard key={instance.id} instance={instance} hubVersion={hubVersion} onOpen={() => onOpen(instance.id)} />
            ))}
          </Section>
        )}
        {working.length > 0 && (
          <Section title="Working" count={working.length}>
            {working.map((instance) => (
              <WorkingCard key={instance.id} instance={instance} hubVersion={hubVersion} onOpen={() => onOpen(instance.id)} />
            ))}
          </Section>
        )}
        {unread.length > 0 && (
          <Section title="Finished since you looked" count={unread.length}>
            {unread.map((instance) => (
              <FinishedCard key={instance.id} instance={instance} hubVersion={hubVersion} onOpen={() => onOpen(instance.id)} />
            ))}
          </Section>
        )}
        {needs.length === 0 && working.length === 0 && unread.length === 0 && (
          <div className="mt-16 flex flex-col items-center text-center">
            <Constellation className="w-56 opacity-80" />
            <p className="mt-6 text-sm text-muted-foreground">Nothing waits for you. Pick an agent to look in on it.</p>
          </div>
        )}

        <VersionsCard root={root} instances={instances} hubVersion={hubVersion} />
      </div>
    </div>
  );
}


// Onboarding shows how to connect the first agents: once per machine.
export function Onboarding({root, onAdd, compact = false}: {root: string; onAdd: () => void; compact?: boolean}) {
  const steps: {title: string; body: string; join?: boolean}[] = [
    {
      title: "Join each machine",
      body: "Once per machine: run the command there, or give the prompt to its agent. It writes the hub into ~/.agentapi/config.yaml, so the agents join on every start.",
      join: true,
    },
    {title: "Restart the agents once", body: "Agents running already show up within a second; ones started before 1.2 join on their next start."},
  ];
  return (
    <div className={cn("hub-rise", compact ? "px-4 py-6" : "")}>
      {compact && (
        <>
          <p className="text-base font-semibold tracking-tight">Connect your first agent</p>
          <p className="mt-1 text-sm text-muted-foreground">Each machine joins once; its agents stay after restarts.</p>
        </>
      )}
      <ol className={cn("space-y-3", compact ? "mt-5" : "mt-8")}>
        {steps.map((step, i) => (
          <li key={step.title} className="flex gap-3.5 rounded-2xl border bg-card p-4 shadow-xs">
            <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-foreground text-xs font-semibold text-background tabular-nums">{i + 1}</span>
            <div className="min-w-0 flex-1">
              <p className="text-sm font-semibold">{step.title}</p>
              <p className="mt-0.5 text-sm text-muted-foreground">{step.body}</p>
              {step.join && (
                <div className="mt-4">
                  <JoinMachine root={root} />
                </div>
              )}
            </div>
          </li>
        ))}
      </ol>
      <p className="mt-5 text-sm text-muted-foreground">
        Can&apos;t run <code className="font-mono text-[12.5px]">hub join</code> there?{" "}
        <button type="button" onClick={onAdd} className="font-medium text-foreground underline underline-offset-4 hover:no-underline">
          Add an agent by its URL
        </button>
        .
      </p>
    </div>
  );
}
