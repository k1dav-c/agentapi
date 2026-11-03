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
  groupSubtotals,
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
  type GroupSubtotal,
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

// CardHeading is a card's title, with a note on what it counts.
function CardHeading({id, title, children}: {id: string; title: string; children: React.ReactNode}) {
  return (
    <h3 id={id} className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
      {title}
      <span className="font-normal tracking-normal normal-case text-muted-foreground/70">{children}</span>
    </h3>
  );
}

// Figure is a card's total, large; title says it in full.
function Figure({label, value, title}: {label: string; value: string; title?: string}) {
  return (
    <div title={title}>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-3xl font-semibold tracking-tight tabular-nums">{value}</dd>
    </div>
  );
}

// PeriodCard is a card of figures of the last 24 hours and 7 days: the
// totals and who counts on the left, its tables on the right.
function PeriodCard({
  id,
  title,
  note,
  day,
  week,
  counted,
  children,
}: {
  id: string;
  title: string;
  note: string;
  day: React.ReactNode;
  week: React.ReactNode;
  counted: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="mt-10" aria-labelledby={id}>
      <CardHeading id={id} title={title}>
        {note}
      </CardHeading>
      <div className="mt-3 grid gap-6 rounded-2xl border bg-card p-5 shadow-xs md:grid-cols-[auto_minmax(0,1fr)]">
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3 md:flex-col md:items-start md:border-r md:pr-8">
          <dl className="flex gap-8 md:flex-col md:gap-4">
            {day}
            {week}
          </dl>
          <p className="text-xs text-muted-foreground">{counted}</p>
        </div>
        {children}
      </div>
    </section>
  );
}

// PeriodTable is a table of the last 24 hours and 7 days, its first column
// headed first.
function PeriodTable({first, children}: {first: string; children: React.ReactNode}) {
  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="text-left text-xs text-muted-foreground">
          <th className="pb-2 font-normal">{first}</th>
          <th className="pb-2 text-right font-normal">24 hours</th>
          <th className="pb-2 text-right font-normal">7 days</th>
        </tr>
      </thead>
      <tbody>{children}</tbody>
    </table>
  );
}

// PeriodRow is a row of a PeriodTable: what it counts (cell, its first
// cell) and its figures; title says them in full.
function PeriodRow({cell, day, week, title}: {cell: React.ReactNode; day: string; week: string; title: string}) {
  return (
    <tr className="border-t" title={title}>
      {cell}
      <td className="py-2 text-right tabular-nums">{day}</td>
      <td className="py-2 pl-4 text-right tabular-nums">{week}</td>
    </tr>
  );
}

// InstanceCell names an instance in a PeriodRow, a button that opens it.
function InstanceCell({instance, onOpen}: {instance: HubInstance; onOpen: (id: string) => void}) {
  return (
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
  );
}

// A row of the By group table, its figures formatted.
interface GroupRow {
  name: string;
  day: string;
  week: string;
  title: string;
}

function groupRows(groups: GroupSubtotal[], format: (n: number) => string, title: (day: number, week: number) => string): GroupRow[] {
  return groups.map((group) => ({name: group.name, day: format(group.day), week: format(group.week), title: title(group.day, group.week)}));
}

// GroupTable is the By group table; none without groups.
function GroupTable({groups}: {groups: GroupRow[]}) {
  if (groups.length === 0) return null;
  return (
    <PeriodTable first="By group">
      {groups.map((group) => (
        <PeriodRow
          key={group.name}
          cell={<td className={cn("max-w-0 truncate py-2 pr-3", group.name ? "font-medium" : "text-muted-foreground")}>{group.name || "Ungrouped"}</td>}
          day={group.day}
          week={group.week}
          title={group.title}
        />
      ))}
    </PeriodTable>
  );
}

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
    <PeriodCard
      id="hub-spend"
      title="Spend"
      note="API-equivalent, at list prices; a subscription doesn't charge it"
      day={<Figure label="Last 24 hours" value={formatUSD(totals.day)} />}
      week={<Figure label="Last 7 days" value={formatUSD(totals.week)} />}
      counted={
        <>
          {totals.counted} of {instances.length} agents report it
        </>
      }
    >
      <PeriodTable first="Spent the most this week">
        {top.map(({instance, spend}) => (
          <PeriodRow
            key={instance.id}
            cell={<InstanceCell instance={instance} onOpen={onOpen} />}
            day={formatUSD(spend.day_usd)}
            week={formatUSD(spend.week_usd)}
            title={spendTitle(spend.day_usd, spend.week_usd)}
          />
        ))}
      </PeriodTable>
    </PeriodCard>
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
  const groups = groupSubtotals(counted.map(({instance, day, week}) => ({instance, day: day.total, week: week.total})));
  return (
    <PeriodCard
      id="hub-tokens"
      title="Tokens"
      note="The calls the spend adds up, models without a price included"
      day={<Figure label="Last 24 hours" value={formatTokens(totals.day.total)} title={`${totals.day.total.toLocaleString()} tokens`} />}
      week={<Figure label="Last 7 days" value={formatTokens(totals.week.total)} title={`${totals.week.total.toLocaleString()} tokens`} />}
      counted={
        <>
          {totals.counted} of {instances.length} agents report it
        </>
      }
    >
      <div className="min-w-0 space-y-5">
        <PeriodTable first="By kind">
          {tokenKinds.map(({key, label}) => (
            <PeriodRow
              key={key}
              cell={<td className="py-2 pr-3">{label}</td>}
              day={formatTokens(totals.day[key])}
              week={formatTokens(totals.week[key])}
              title={tokensTitle(totals.day[key], totals.week[key])}
            />
          ))}
        </PeriodTable>
        <PeriodTable first="Used the most this week">
          {top.map(({instance, day, week}) => (
            <PeriodRow
              key={instance.id}
              cell={<InstanceCell instance={instance} onOpen={onOpen} />}
              day={formatTokens(day.total)}
              week={formatTokens(week.total)}
              title={tokensTitle(day.total, week.total)}
            />
          ))}
        </PeriodTable>
        <GroupTable groups={groupRows(groups, formatTokens, tokensTitle)} />
      </div>
    </PeriodCard>
  );
}

// The work time the card shows at a moment, its figures formatted: it
// renders again when one of them changes, not at every tick of the clock.
interface WorkView {
  day: string;
  week: string;
  counted: number;
  top: {id: string; day: string; week: string; title: string}[];
  groups: GroupRow[];
}

function workView(instances: HubInstance[], now: number): WorkView | undefined {
  const totals = workTotals(instances, now);
  if (totals.week <= 0) return undefined;
  const each = instances.map((instance) => ({instance, ...workSeconds(instance, now)}));
  const top = each
    .filter(({week}) => week > 0)
    .sort((a, b) => b.week - a.week)
    .slice(0, 5)
    .map(({instance, day, week}) => ({id: instance.id, day: formatWork(day), week: formatWork(week), title: workTitle(day, week)}));
  return {day: formatWork(totals.day), week: formatWork(totals.week), counted: totals.counted, top, groups: groupRows(groupSubtotals(each), formatWork, workTitle)};
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
    <PeriodCard
      id="hub-work"
      title="Work time"
      note="How long the agents worked, as the hub saw it"
      day={<Figure label="Last 24 hours" value={view.day} />}
      week={<Figure label="Last 7 days" value={view.week} />}
      counted={
        <>
          {view.counted} of {instances.length} agents worked this week
        </>
      }
    >
      <div className="min-w-0 space-y-5">
        <PeriodTable first="Worked the most this week">
          {view.top.map((row) => {
            const instance = byID.get(row.id);
            if (!instance) return null;
            return (
              <PeriodRow
                key={row.id}
                cell={<InstanceCell instance={instance} onOpen={onOpen} />}
                day={row.day}
                week={row.week}
                title={row.title}
              />
            );
          })}
        </PeriodTable>
        <GroupTable groups={view.groups} />
      </div>
    </PeriodCard>
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
      <CardHeading id="hub-versions" title="Versions">
        {release ? `The AgentAPI version of each agent; the hub runs v${hubVersion}` : "The hub is a development build, so no version counts as older"}
      </CardHeading>
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
