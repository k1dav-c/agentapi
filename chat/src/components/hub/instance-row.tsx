"use client";

import {memo, useState} from "react";
import {ArrowDown, ArrowUp, Bell, BellOff, ChevronRight, Folder, FolderPlus, MoreHorizontal, Pencil, Pin, PinOff, Timer, Trash2} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {Button} from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {agentDisplayName, parseAgentType} from "@/lib/agents";
import {displayName, finishedAt, instanceState, stateLabel, usagePercent, type HubInstance} from "@/lib/hub";
import {activity, formatAgo} from "@/lib/hub-format";
import {groupOf, type HubGroup} from "@/lib/hub-groups";
import {formatUSD, spendTitle, spendTotals, tokenTotals} from "@/lib/hub-spend";
import {instanceVersion, olderThanHub} from "@/lib/hub-version";
import {formatWork, workSeconds, workTitle} from "@/lib/hub-work";
import {changedAt, type ListOrder} from "@/lib/hub-order";
import {shownUpdate, updateBadge} from "@/lib/hub-update";
import {cn} from "@/lib/utils";
import {StatusGlyph, stateTone} from "./status-glyph";
import {UpdateDialog, UpdateMenuItems, cancelFromHub} from "./update";
import {UpdateChip} from "./update-chip";
import {useHubTime} from "./clock";
import {useHubList, type PinPlace} from "./list-context";
import {JobChip} from "./issues/job-chip";
import {VersionTag} from "./version-tag";

// activityTone is the color of the line that says what a row's instance does.
export const activityTone = {
  needs: "text-state-needs",
  working: "text-foreground/80",
  ready: "text-foreground/80",
  fault: "text-state-fault",
  muted: "text-muted-foreground",
} as const;

// UsageBar is the fullest usage limit, as a short bar and a percentage.
export function UsageBar({percent}: {percent: number}) {
  const tone = percent >= 90 ? "bg-state-fault" : percent >= 70 ? "bg-state-needs" : "bg-muted-foreground/45";
  return (
    <span className="flex shrink-0 items-center gap-1.5 tabular-nums" title={`Usage limit ${Math.round(percent)}% used`}>
      <span className="relative h-1.5 w-10 overflow-hidden rounded-full bg-muted" aria-hidden="true">
        <span className={cn("absolute inset-y-0 left-0 rounded-full", tone)} style={{width: `${Math.min(100, Math.max(4, percent))}%`}} />
      </span>
      <span>{Math.round(percent)}%</span>
    </span>
  );
}

// agentName names the instance's agent; an agent this build doesn't know
// (one an AgentAPI 2 ran, e.g. "aider") by its type.
export function agentName(instance: HubInstance): string {
  if (!instance.agent_type) return "";
  const type = parseAgentType(instance.agent_type);
  return type === "custom" && instance.agent_type !== "custom" ? instance.agent_type : agentDisplayName(type, instance.agent_type);
}

export interface InstanceRowProps {
  instance: HubInstance;
  // order is the list's: in the recent order, with no group headers, the
  // row names its group, and its age is that of its latest change.
  order: ListOrder;
  selected: boolean;
  // pinned is the row's place in the pinned section, when it is pinned
  // (pinPlace). The row names its group, as in the recent order.
  pinned?: PinPlace;
}

// InstanceRow is an instance in the hub's list; what every row shares
// comes from HubListContext. Its age reads the hub's clock (useHubTime):
// the row renders again when the age it shows changes, not on every tick.
export const InstanceRow = memo(function InstanceRow({instance, order, selected, pinned}: InstanceRowProps) {
  const {hubVersion, actions} = useHubList();
  const state = instanceState(instance);
  const summary = instance.summary;
  const usage = usagePercent(summary);
  const usageShown = usage !== undefined && usage >= 60;
  const agent = agentName(instance);
  const doing = activity(instance);
  const recent = order === "recent";
  const since = recent ? changedAt(instance) : doing.since;
  const ago = useHubTime((now) => formatAgo(since, now));
  const group = recent || pinned ? groupOf(instance) : "";
  const spend = summary?.spend;
  // The last spend and version of an instance the hub doesn't follow now:
  // shown, not counted.
  const lastKnown = instance.connection !== "connected";
  const version = instanceVersion(instance);
  const update = shownUpdate(instance);
  const id = instance.id;
  return (
    <li className="group/row relative px-2">
      <div
        className={cn(
          "flex flex-wrap items-stretch rounded-xl transition-colors",
          selected ? "bg-accent ring-1 ring-border" : "hover:bg-accent/60",
        )}
      >
        {selected && <span aria-hidden="true" className="absolute inset-y-3 left-2 w-[3px] rounded-r-full bg-state-working" />}
        <button
          type="button"
          data-hub-row={instance.id}
          onClick={() => actions.select(id)}
          aria-current={selected ? "true" : undefined}
          aria-label={`${displayName(instance)}, ${stateLabel(instance)}${ago ? `, ${ago}` : ""}: ${doing.text}${olderThanHub(instance, hubVersion) ? ". Older than the hub" : ""}${update ? `. ${updateBadge(update).title}` : ""}${pinned ? ". Pinned" : ""}${group ? `. Group: ${group}` : ""}`}
          className="flex min-w-0 flex-1 items-start gap-3 rounded-xl py-3 pr-1 pl-3.5 text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <StatusGlyph state={state} finished={finishedAt(instance)} className="mt-[3px]" />
          <span className="min-w-0 flex-1">
            <span className="flex items-baseline gap-2">
              <span className="min-w-0 truncate text-[15px] leading-6 font-medium">{displayName(instance)}</span>
              {pinned && <Pin aria-hidden="true" className="size-3 shrink-0 self-center fill-current text-muted-foreground/70" />}
              {version && <VersionTag version={version} hubVersion={hubVersion} lastKnown={lastKnown} className="text-xs text-muted-foreground" />}
              {group && <GroupTag group={group} />}
              {ago && <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">{ago}</span>}
            </span>
            <span className={cn("block truncate text-[13px] leading-5", activityTone[doing.tone])}>
              {doing.label && (
                <>
                  <span className={cn("font-medium", stateTone[state])}>{doing.label}</span>
                  <span className="text-muted-foreground/60"> · </span>
                </>
              )}
              {doing.text}
            </span>
            <span className="mt-1.5 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
              {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3" />}
              {agent && <span className="shrink-0">{agent}</span>}
              {instance.hostname && (
                <>
                  <span aria-hidden="true" className="text-muted-foreground/40">·</span>
                  <span className="min-w-0 truncate" data-hub-host>
                    {instance.hostname}
                  </span>
                </>
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
              {!usageShown && <RowWork instance={instance} />}
              {summary && summary.queued_messages > 0 && (
                <span className="shrink-0 rounded-full bg-muted px-1.5 py-px font-medium text-foreground/80">{summary.queued_messages} queued</span>
              )}
              {usageShown && (
                <span className="ml-auto pl-1">
                  <UsageBar percent={usage} />
                </span>
              )}
            </span>
            {/* A line of its own, so the line above keeps the machine's name. */}
            {update && (
              <span className="mt-1.5 flex text-xs">
                <UpdateChip update={update} />
              </span>
            )}
          </span>
        </button>
        <RowMenu instance={instance} pinned={pinned} />
        {/* Below the row's button: a button of its own. */}
        <JobChip instanceID={id} name={displayName(instance)} className="-mt-1.5 pr-3 pb-2.5 pl-[46px]" />
      </div>
    </li>
  );
});

// RowMenu is a row's ⋯ menu: pin, mute, its group, update and forget, and
// the update's dialog. pinned is the row's place among the pinned ones, as
// InstanceRow has it; className sizes its button.
export function RowMenu({instance, pinned, className}: Pick<InstanceRowProps, "instance" | "pinned"> & {className?: string}) {
  const {hubVersion, root, groups, actions} = useHubList();
  const state = instanceState(instance);
  const gone = state === "stopped" || state === "unreachable";
  const id = instance.id;
  const [updating, setUpdating] = useState(false);
  return (
    <>
      {/* Not modal: a modal menu that opens the naming dialog leaves the page
          unclickable once both close (Radix's pointer-events lock). */}
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className={cn(
              "my-auto mr-1 size-9 shrink-0 rounded-lg text-muted-foreground transition-opacity focus-visible:opacity-100 group-hover/row:opacity-100 data-[state=open]:opacity-100 [@media(hover:hover)]:opacity-0",
              className,
            )}
            aria-label={`Actions for ${displayName(instance)}`}
            data-hub-row-menu={instance.id}
          >
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60 max-w-[calc(100vw-1rem)]">
          {pinned ? (
            <>
              <DropdownMenuItem onSelect={() => actions.pin(id, false)}>
                <PinOff /> Unpin
              </DropdownMenuItem>
              <DropdownMenuItem disabled={pinned.first} onSelect={() => actions.move(id, -1)}>
                <ArrowUp /> Move up
              </DropdownMenuItem>
              <DropdownMenuItem disabled={pinned.last} onSelect={() => actions.move(id, 1)}>
                <ArrowDown /> Move down
              </DropdownMenuItem>
            </>
          ) : (
            <DropdownMenuItem onSelect={() => actions.pin(id, true)}>
              <Pin /> Pin to top
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          {(state === "needs-you" || state === "muted") && (
            <>
              <DropdownMenuItem onSelect={() => actions.mute(id, state === "needs-you")}>
                {state === "needs-you" ? <BellOff /> : <Bell />}
                {state === "needs-you" ? "Mute until it changes" : "Unmute"}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
            </>
          )}
          <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Move to group</DropdownMenuLabel>
          <DropdownMenuRadioGroup value={groupOf(instance)} onValueChange={(name) => actions.group(id, name)}>
            <DropdownMenuRadioItem value="">No group</DropdownMenuRadioItem>
            {groups.map((name) => (
              <DropdownMenuRadioItem key={name} value={name}>
                <span className="truncate">{name}</span>
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
          <DropdownMenuItem onSelect={() => actions.newGroup(id)}>
            <FolderPlus /> New group…
          </DropdownMenuItem>
          <UpdateMenuItems instance={instance} onOpen={() => setUpdating(true)} onCancel={() => void cancelFromHub(root, instance.id)} />
          {gone && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" onSelect={() => actions.forget(id)}>
                <Trash2 /> Forget
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      <UpdateDialog instance={instance} hubVersion={hubVersion} root={root} open={updating} onOpenChange={setUpdating} />
    </>
  );
}

// GroupTag names a row's group in the recent order and among the pinned
// ones, in the room its first line leaves before the age: whole, cut short,
// or not at all where not even its start fits (it wraps to a second line,
// which its height leaves out).
function GroupTag({group}: {group: string}) {
  return (
    // Its own padding is the gap before it, so that out of sight it takes
    // no room from the name.
    <span className="-ml-2 flex h-4 min-w-0 flex-1 flex-wrap items-baseline justify-end overflow-hidden text-xs leading-4 text-muted-foreground">
      {/* What the tag wraps after; a zero-width space keeps the line's baseline. */}
      <span aria-hidden="true">{"\u200b"}</span>
      <span className="max-w-full min-w-18 truncate pl-2" title={`Group: ${group}`}>
        <Folder className="mr-1 inline size-3 align-[-2px]" aria-hidden="true" />
        {group}
      </span>
    </span>
  );
}

// GroupHeader heads a section of the list with its label: a group (its
// pencil renames it), the pinned ones, Ungrouped or the recent order's rows.
export function GroupHeader({group, label, folded, onToggle, onRename}: {group: HubGroup; label: string; folded: boolean; onToggle: () => void; onRename: () => void}) {
  return (
    <div className="sticky top-0 z-10 bg-background px-2 pt-3 pb-1">
      <div className="flex items-center">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={!folded}
          className="flex h-9 min-w-0 flex-1 items-center gap-1.5 rounded-lg px-2 text-left outline-none hover:bg-accent/60 focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", !folded && "rotate-90")} />
          <span className="min-w-0 truncate text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">{label}</span>
          <span className="shrink-0 text-[11px] text-muted-foreground/70 tabular-nums">{group.instances.length}</span>
          <GroupSpend group={group} />
          {group.needsYou > 0 && (
            <span className="ml-1 inline-flex h-[18px] min-w-[18px] shrink-0 items-center justify-center rounded-full bg-state-needs px-1.5 text-[10px] font-semibold text-background tabular-nums">
              <span className="sr-only">{`${group.needsYou} need you`}</span>
              <span aria-hidden="true">{group.needsYou}</span>
            </span>
          )}
        </button>
        {group.name && (
          <Button variant="ghost" size="icon" className="size-9 shrink-0 rounded-lg text-muted-foreground" aria-label={`Rename the group ${group.name}`} onClick={onRename}>
            <Pencil className="size-3.5" />
          </Button>
        )}
      </div>
    </div>
  );
}

// RowWork is how long the instance worked in the last 24 hours, as the hub
// saw it, while it worked in the last 7 days: a timer and the figure. The
// machine's name gives way to it. It reads the hub's clock for
// the span going on: it renders again when its figure changes, the row
// doesn't.
export function RowWork({instance}: {instance: HubInstance}) {
  const title = useHubTime((now) => {
    const seconds = workSeconds(instance, now);
    return seconds.week > 0 ? workTitle(seconds.day, seconds.week) : "";
  });
  const day = useHubTime((now) => formatWork(workSeconds(instance, now).day));
  if (!title) return null;
  return (
    <span className="inline-flex shrink-0 items-center gap-1 tabular-nums" title={title} data-hub-work>
      <span className="sr-only">{title}</span>
      <Timer aria-hidden="true" className="size-3 text-muted-foreground/70" />
      <span aria-hidden="true">{day}</span>
    </span>
  );
}

// GroupSpend is what a group's instances spent in the last 24 hours.
function GroupSpend({group}: {group: HubGroup}) {
  const totals = spendTotals(group.instances);
  if (totals.week <= 0) return null;
  const tokens = tokenTotals(group.instances);
  const title = spendTitle(totals.day, totals.week, false, tokens.counted > 0 ? tokens.day.total : undefined);
  return (
    <span className="shrink-0 text-[11px] text-muted-foreground/70 tabular-nums" title={title}>
      <span className="sr-only">{title}</span>
      <span aria-hidden="true">· {formatUSD(totals.day)}/24h</span>
    </span>
  );
}
