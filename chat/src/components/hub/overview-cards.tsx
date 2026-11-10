"use client";

// The overview's cards of the agents in all: what they spent, the tokens
// they used, how long they worked, and the AgentAPI versions they run.

import {ChevronRight} from "lucide-react";
import {Fragment} from "react";
import {AgentMark} from "@/components/agent-mark";
import {displayName, type HubInstance} from "@/lib/hub";
import {groupSubtotals, type GroupSubtotal} from "@/lib/hub-groups";
import {machineOf, machineSubtotals, type MachineSubtotal} from "@/lib/hub-machines";
import {formatTokens, formatUSDShort} from "@/lib/format";
import {countedSpends, countedTokens, spendTitle, spendTotals, tokenKinds, tokensTitle, tokenTotals} from "@/lib/hub-spend";
import {parseVersion, versionTotals, type VersionRelation} from "@/lib/hub-version";
import {formatWork, workSeconds, workTitle, workTotals} from "@/lib/hub-work";
import {olderUpdates} from "@/lib/hub-update";
import {cn} from "@/lib/utils";
import {useHubTime} from "./clock";
import {UpdateOlder} from "./update";
import {OlderMark} from "./version-tag";

// CardHeading is a card's title, with a note on what it counts.
function CardHeading({id, title, children}: {id: string; title: string; children: React.ReactNode}) {
  return (
    <h3 id={id} className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
      {title}
      <span className="font-normal tracking-normal normal-case text-muted-foreground/70">{children}</span>
    </h3>
  );
}

// Fold is whether a card is folded, and what folds or unfolds it; the
// overview keeps it in the browser (overviewFolded).
export interface Fold {
  folded: boolean;
  onToggle: () => void;
}

// FoldHeading is the title of a card that folds: a button that folds it,
// the note, and while it is folded, its headline figure.
export function FoldHeading({
  id,
  title,
  note,
  fold,
  headline,
  children,
}: {
  id: string;
  title: string;
  note: string;
  fold?: Fold;
  headline: string;
  children?: React.ReactNode;
}) {
  if (!fold) {
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <CardHeading id={id} title={title}>
          {note}
        </CardHeading>
        {children}
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <h3 id={id} className="flex min-w-0 items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
        <button
          type="button"
          onClick={fold.onToggle}
          aria-expanded={!fold.folded}
          aria-controls={`${id}-body`}
          className="-ml-1 flex items-center gap-1 rounded px-1 py-0.5 uppercase outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 transition-transform", !fold.folded && "rotate-90")} />
          {title}
        </button>
        <span className="hidden truncate font-normal tracking-normal normal-case text-muted-foreground/70 sm:inline">{note}</span>
      </h3>
      {fold.folded ? <span className="ml-auto text-sm font-semibold tabular-nums">{headline}</span> : children}
    </div>
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
// Folded, it shows only its title and headline, the 7 days' figure.
function PeriodCard({
  id,
  title,
  note,
  day,
  week,
  headline,
  counted,
  fold,
  children,
}: {
  id: string;
  title: string;
  note: string;
  day: React.ReactNode;
  week: React.ReactNode;
  headline: string;
  counted: React.ReactNode;
  fold?: Fold;
  children: React.ReactNode;
}) {
  return (
    <section className="mt-10" aria-labelledby={id}>
      <FoldHeading id={id} title={title} note={note} fold={fold} headline={`${headline} in 7 days`} />
      {!fold?.folded && (
      <div id={`${id}-body`} className="mt-3 grid gap-6 rounded-2xl border bg-card p-5 shadow-xs md:grid-cols-[auto_minmax(0,1fr)]">
        <div className="flex flex-wrap items-end gap-x-8 gap-y-3 md:flex-col md:items-start md:border-r md:pr-8">
          <dl className="flex gap-8 md:flex-col md:gap-4">
            {day}
            {week}
          </dl>
          <p className="text-xs text-muted-foreground">{counted}</p>
        </div>
        {children}
      </div>
      )}
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

// InstanceCell names an instance in a PeriodRow, a button that opens it,
// and its machine, which tells apart instances of the same name.
function InstanceCell({instance, onOpen}: {instance: HubInstance; onOpen: (id: string) => void}) {
  const name = displayName(instance);
  const machine = machineOf(instance);
  return (
    <td className="max-w-0 py-2 pr-3">
      <button
        type="button"
        onClick={() => onOpen(instance.id)}
        className="group/cell flex w-full min-w-0 items-baseline gap-2 rounded text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3.5 shrink-0 self-center" />}
        <span className="max-w-[60%] shrink-0 truncate font-medium group-hover/cell:underline">{name}</span>
        {machine && machine !== name && (
          <span className="min-w-0 truncate text-xs text-muted-foreground" title={machine} data-hub-host>
            {machine}
          </span>
        )}
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

// A row of the By machine table, its figures formatted.
interface MachineRow extends GroupRow {
  others?: number;
}

function machineRows(machines: MachineSubtotal[], format: (n: number) => string, title: (day: number, week: number) => string): MachineRow[] {
  return machines.map((machine) => ({...groupRows([machine], format, title)[0], others: machine.others}));
}

// MachineTable is the By machine table, the most this week first; none
// while the agents run on one machine.
function MachineTable({machines}: {machines: MachineRow[]}) {
  if (machines.length === 0) return null;
  return (
    <PeriodTable first="By machine">
      {machines.map((machine) => (
        <PeriodRow
          key={machine.others ? " others" : machine.name}
          cell={
            <td className={cn("max-w-0 truncate py-2 pr-3", machine.name ? "font-medium" : "text-muted-foreground")}>
              {machine.others ? `Other ${machine.others} machines` : machine.name ? <span data-hub-host>{machine.name}</span> : "Not reported"}
            </td>
          }
          day={machine.day}
          week={machine.week}
          title={machine.title}
        />
      ))}
    </PeriodTable>
  );
}

// SpendCard is what the instances spent in the last 24 hours and 7 days,
// all of them, the ones that spent the most this week, and each machine.
export function SpendCard({instances, onOpen, fold}: {instances: HubInstance[]; onOpen: (id: string) => void; fold?: Fold}) {
  const totals = spendTotals(instances);
  if (totals.week <= 0) return null;
  // Each scope once, as the totals count them; a scope is of one machine.
  const counted = countedSpends(instances);
  const top = counted
    .filter(({spend}) => spend.week_usd > 0)
    .sort((a, b) => b.spend.week_usd - a.spend.week_usd)
    .slice(0, 5);
  const machines = machineSubtotals(counted.map(({instance, spend}) => ({instance, day: spend.day_usd, week: spend.week_usd})));
  return (
    <PeriodCard
      id="hub-spend"
      title="Spend"
      note="API-equivalent, at list prices; a subscription doesn't charge it"
      day={<Figure label="Last 24 hours" value={formatUSDShort(totals.day)} />}
      week={<Figure label="Last 7 days" value={formatUSDShort(totals.week)} />}
      headline={formatUSDShort(totals.week)}
      fold={fold}
      counted={
        <>
          {totals.counted} of {instances.length} agents report it
        </>
      }
    >
      <div className="min-w-0 space-y-5">
        <PeriodTable first="Spent the most this week">
          {top.map(({instance, spend}) => (
            <PeriodRow
              key={instance.id}
              cell={<InstanceCell instance={instance} onOpen={onOpen} />}
              day={formatUSDShort(spend.day_usd)}
              week={formatUSDShort(spend.week_usd)}
              title={spendTitle(spend.day_usd, spend.week_usd)}
            />
          ))}
        </PeriodTable>
        <MachineTable machines={machineRows(machines, formatUSDShort, (day, week) => spendTitle(day, week))} />
      </div>
    </PeriodCard>
  );
}

// TokensCard is the tokens the instances used in the last 24 hours and 7
// days, the calls their spend adds up: all of them by kind, the ones that
// used the most this week, each group and each machine.
export function TokensCard({instances, onOpen, fold}: {instances: HubInstance[]; onOpen: (id: string) => void; fold?: Fold}) {
  const totals = tokenTotals(instances);
  if (totals.week.total <= 0) return null;
  // Each scope once, as the totals count them.
  const counted = countedTokens(instances);
  const top = counted
    .filter(({week}) => week.total > 0)
    .sort((a, b) => b.week.total - a.week.total)
    .slice(0, 5);
  const totalsOf = counted.map(({instance, day, week}) => ({instance, day: day.total, week: week.total}));
  const groups = groupSubtotals(totalsOf);
  const machines = machineSubtotals(totalsOf);
  return (
    <PeriodCard
      id="hub-tokens"
      title="Tokens"
      note="The calls the spend adds up, models without a price included"
      day={<Figure label="Last 24 hours" value={formatTokens(totals.day.total)} title={`${totals.day.total.toLocaleString()} tokens`} />}
      week={<Figure label="Last 7 days" value={formatTokens(totals.week.total)} title={`${totals.week.total.toLocaleString()} tokens`} />}
      headline={`${formatTokens(totals.week.total)} tokens`}
      fold={fold}
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
        <MachineTable machines={machineRows(machines, formatTokens, tokensTitle)} />
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
  machines: MachineRow[];
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
  return {
    day: formatWork(totals.day),
    week: formatWork(totals.week),
    counted: totals.counted,
    top,
    groups: groupRows(groupSubtotals(each), formatWork, workTitle),
    machines: machineRows(machineSubtotals(each), formatWork, workTitle),
  };
}

const sameView = (a: WorkView | undefined, b: WorkView | undefined) => JSON.stringify(a) === JSON.stringify(b);

// WorkCard is how long the agents worked in the last 24 hours and 7 days,
// as the hub saw it: all of them, the ones that worked the most this week,
// each group and each machine. It reads the hub's clock for the spans going
// on.
export function WorkCard({instances, onOpen, fold}: {instances: HubInstance[]; onOpen: (id: string) => void; fold?: Fold}) {
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
      headline={view.week}
      fold={fold}
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
        <MachineTable machines={view.machines} />
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
