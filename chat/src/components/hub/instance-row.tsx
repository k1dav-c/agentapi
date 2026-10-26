"use client";

import {ChevronRight, FolderPlus, MoreHorizontal, Pencil, Trash2} from "lucide-react";
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
import {activity, displayName, formatAgo, instanceState, stateLabel, usagePercent, type HubGroup, type HubInstance} from "@/lib/hub";
import {cn} from "@/lib/utils";
import {StatusGlyph, stateTone} from "./status-glyph";

const activityTone = {
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

export function agentName(instance: HubInstance): string {
  return instance.agent_type ? agentDisplayName(parseAgentType(instance.agent_type), instance.agent_type) : "";
}

export function InstanceRow({
  instance,
  groups,
  selected,
  now,
  onSelect,
  onForget,
  onGroup,
  onNewGroup,
}: {
  instance: HubInstance;
  groups: string[];
  selected: boolean;
  now: number;
  onSelect: () => void;
  onForget: () => void;
  onGroup: (name: string) => void;
  onNewGroup: () => void;
}) {
  const state = instanceState(instance);
  const summary = instance.summary;
  const usage = usagePercent(summary);
  const agent = agentName(instance);
  const doing = activity(instance);
  const ago = formatAgo(doing.since, now);
  const gone = state === "stopped" || state === "unreachable";
  return (
    <li className="group/row relative px-2">
      <div
        className={cn(
          "flex items-stretch rounded-xl transition-colors",
          selected ? "bg-accent ring-1 ring-border" : "hover:bg-accent/60",
        )}
      >
        {selected && <span aria-hidden="true" className="absolute inset-y-3 left-2 w-[3px] rounded-r-full bg-state-working" />}
        <button
          type="button"
          data-hub-row={instance.id}
          onClick={onSelect}
          aria-current={selected ? "true" : undefined}
          aria-label={`${displayName(instance)}, ${stateLabel(instance)}${ago ? `, ${ago}` : ""}: ${doing.text}`}
          className="flex min-w-0 flex-1 items-start gap-3 rounded-xl py-3 pr-1 pl-3.5 text-left outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <StatusGlyph state={state} className="mt-[3px]" />
          <span className="min-w-0 flex-1">
            <span className="flex items-baseline gap-2">
              <span className="min-w-0 flex-1 truncate text-[15px] leading-6 font-medium">{displayName(instance)}</span>
              {ago && <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{ago}</span>}
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
              {summary && summary.queued_messages > 0 && (
                <span className="shrink-0 rounded-full bg-muted px-1.5 py-px font-medium text-foreground/80">{summary.queued_messages} queued</span>
              )}
              {usage !== undefined && usage >= 60 && (
                <span className="ml-auto pl-1">
                  <UsageBar percent={usage} />
                </span>
              )}
            </span>
          </span>
        </button>
        {/* Not modal: a modal menu that opens the naming dialog leaves the page
            unclickable once both close (Radix's pointer-events lock). */}
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="my-auto mr-1 size-9 shrink-0 rounded-lg text-muted-foreground transition-opacity focus-visible:opacity-100 group-hover/row:opacity-100 data-[state=open]:opacity-100 [@media(hover:hover)]:opacity-0"
              aria-label={`Actions for ${displayName(instance)}`}
            >
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-60 max-w-[calc(100vw-1rem)]">
            <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Move to group</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={instance.group?.trim() ?? ""} onValueChange={onGroup}>
              <DropdownMenuRadioItem value="">No group</DropdownMenuRadioItem>
              {groups.map((name) => (
                <DropdownMenuRadioItem key={name} value={name}>
                  <span className="truncate">{name}</span>
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            <DropdownMenuItem onSelect={onNewGroup}>
              <FolderPlus /> New group…
            </DropdownMenuItem>
            {gone && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onSelect={onForget}>
                  <Trash2 /> Forget
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </li>
  );
}

export function GroupHeader({group, folded, onToggle, onRename}: {group: HubGroup; folded: boolean; onToggle: () => void; onRename: () => void}) {
  return (
    <li className="sticky top-0 z-10 bg-background px-2 pt-3 pb-1">
      <div className="flex items-center">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={!folded}
          className="flex h-9 min-w-0 flex-1 items-center gap-1.5 rounded-lg px-2 text-left outline-none hover:bg-accent/60 focus-visible:ring-[3px] focus-visible:ring-ring/50"
        >
          <ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", !folded && "rotate-90")} />
          <span className="min-w-0 truncate text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">{group.name || "Ungrouped"}</span>
          <span className="shrink-0 text-[11px] text-muted-foreground/70 tabular-nums">{group.instances.length}</span>
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
    </li>
  );
}
