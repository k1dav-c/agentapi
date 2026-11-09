"use client";

import {Check, ChevronDown, FolderGit2, Play} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import {agentLabel, type DispatchHost} from "@/lib/hub-dispatch";
import {assignOptions, type Option} from "@/lib/hub-issues";
import {cn} from "@/lib/utils";

// Capacity draws a machine's jobs against its room as pips: filled for a
// job, hollow for room. Past six it says the numbers.
export function Capacity({host, className}: {host: Pick<DispatchHost, "active" | "max_jobs" | "online">; className?: string}) {
  const label = `${host.active} of ${host.max_jobs} jobs`;
  if (host.max_jobs > 6) {
    return (
      <span className={cn("font-mono text-[11px] text-muted-foreground tabular-nums", className)} aria-label={label}>
        {host.active}/{host.max_jobs}
      </span>
    );
  }
  return (
    <span role="img" aria-label={label} className={cn("inline-flex items-center gap-[3px]", className)}>
      {Array.from({length: host.max_jobs}, (_, i) => (
        <span
          key={i}
          className={cn(
            "size-[5px] rounded-full",
            !host.online ? "bg-muted-foreground/25" : i < host.active ? "bg-state-working" : "ring-1 ring-muted-foreground/40 ring-inset",
          )}
        />
      ))}
    </span>
  );
}

// OptionLine is an agent on a machine, as a pick shows it.
function OptionLine({option, suggested}: {option: Option; suggested: boolean}) {
  return (
    <>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5 font-mono text-[13px]">
          <span className="truncate">{option.host.hostname}</span>
          {suggested && <span className="rounded-full bg-foreground/8 px-1.5 font-sans text-[10px] font-medium text-muted-foreground">suggested</span>}
        </span>
        <span className="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground">
          {option.hasRepo ? (
            <>
              <FolderGit2 className="size-3" aria-hidden="true" /> has the repo
            </>
          ) : (
            "clones the repo"
          )}
        </span>
      </span>
      <Capacity host={option.host} />
    </>
  );
}

// AssignMenuItems are the picks for an issue of repo, by agent: a tap
// assigns. The suggestion is marked; the machines that can't take it are
// there, greyed, with why (offline, full, without the agent or the
// repository), so none goes missing without a word.
export function AssignMenuItems({hosts, repo, suggested, onPick}: {hosts: DispatchHost[]; repo: string; suggested?: Option; onPick: (agent: string, host: string) => void}) {
  const groups = assignOptions(hosts, repo);
  if (groups.length === 0) return <p className="px-3 py-4 text-sm text-muted-foreground">No machine online runs an agent.</p>;
  return groups.map(({agent, options, unavailable}, i) => (
    <DropdownMenuGroup key={agent}>
      {i > 0 && <DropdownMenuSeparator />}
      <DropdownMenuLabel className="flex items-center gap-2 pt-2 text-xs font-medium">
        <AgentMark agentType={agent} className="size-3.5" />
        {agentLabel(agent)}
        {options.length === 0 && <span className="ml-auto font-normal text-muted-foreground">no machine can take it</span>}
      </DropdownMenuLabel>
      {options.map((option) => {
        const isSuggested = suggested?.agent === agent && suggested.host.hostname === option.host.hostname;
        return (
          <DropdownMenuItem
            key={option.host.hostname}
            className="mx-1 gap-3 rounded-lg py-2 pr-2.5 pl-8"
            onSelect={() => onPick(agent, option.host.hostname)}
            aria-label={`${agentLabel(agent)} on ${option.host.hostname}`}
          >
            <OptionLine option={option} suggested={isSuggested} />
          </DropdownMenuItem>
        );
      })}
      {unavailable.map(({host, reason}) => (
        <DropdownMenuItem
          key={host.hostname}
          disabled
          className="mx-1 gap-3 rounded-lg py-1.5 pr-2.5 pl-8 data-[disabled]:opacity-100"
          aria-label={`${agentLabel(agent)} on ${host.hostname}: ${reason}`}
        >
          <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-muted-foreground/70">{host.hostname}</span>
          <span className="shrink-0 text-[11px] text-muted-foreground">{reason}</span>
        </DropdownMenuItem>
      ))}
    </DropdownMenuGroup>
  ));
}

// AssignButton is one tap to the suggestion, and beside it the picks: an
// agent on a machine, for when the suggestion isn't it. Without a machine
// to take it, it says why.
export function AssignButton({
  hosts,
  repo,
  suggested,
  busy,
  disabledReason,
  onAssign,
  label = "Assign",
  size = "sm",
  className,
}: {
  hosts: DispatchHost[];
  repo: string;
  suggested?: Option;
  busy: boolean;
  disabledReason?: string;
  onAssign: (agent: string, host: string, picked: boolean) => void;
  label?: string;
  size?: "sm" | "lg";
  className?: string;
}) {
  const disabled = busy || !suggested;
  const height = size === "lg" ? "h-11" : "h-8";
  return (
    <div className={cn("relative z-10 inline-flex shrink-0 rounded-lg shadow-xs", disabled && "shadow-none", className)} role="group" aria-label={label}>
      <button
        type="button"
        disabled={disabled}
        title={suggested ? `${label} to ${agentLabel(suggested.agent)} on ${suggested.host.hostname}` : disabledReason}
        aria-label={suggested ? `${label} to ${agentLabel(suggested.agent)} on ${suggested.host.hostname}` : `${label}: ${disabledReason ?? "no machine can take it"}`}
        onClick={() => suggested && onAssign(suggested.agent, suggested.host.hostname, false)}
        className={cn(
          "group/assign inline-flex items-center gap-2 rounded-l-lg bg-primary pr-3 pl-2.5 text-sm font-medium text-primary-foreground outline-none transition-[background-color,transform] hover:bg-primary/88 focus-visible:z-10 focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-[0.98] disabled:cursor-not-allowed disabled:bg-muted disabled:text-muted-foreground",
          height,
        )}
      >
        {busy ? (
          <span className="size-3.5 animate-spin rounded-full border-[1.5px] border-current border-t-transparent" aria-hidden="true" />
        ) : suggested ? (
          <span className="flex size-[18px] items-center justify-center rounded-[5px] bg-primary-foreground/95">
            <AgentMark agentType={suggested.agent} className="size-3" />
          </span>
        ) : (
          <Play className="size-3.5" aria-hidden="true" />
        )}
        {busy ? "Assigning…" : label}
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger
          disabled={busy || hosts.every((host) => !host.online)}
          aria-label="Choose the agent and the machine"
          title="Choose the agent and the machine"
          className={cn(
            "inline-flex items-center justify-center rounded-r-lg border-l border-primary-foreground/15 bg-primary px-1.5 text-primary-foreground outline-none transition-colors hover:bg-primary/88 focus-visible:z-10 focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:border-border disabled:bg-muted disabled:text-muted-foreground data-[state=open]:bg-primary/85",
            height,
          )}
        >
          <ChevronDown className="size-4 transition-transform duration-200 [[data-state=open]>&]:rotate-180" aria-hidden="true" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={6} className="w-80 rounded-xl p-1 shadow-lg">
          <p className="px-3 pt-2 pb-1 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">Assign to</p>
          <AssignMenuItems hosts={hosts} repo={repo} suggested={suggested} onPick={(agent, host) => onAssign(agent, host, true)} />
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

// SwitchMenuItems are the agents a job could switch to on its machine.
export function SwitchMenuItems({agents, onPick}: {agents: string[]; onPick: (agent: string) => void}) {
  return agents.map((agent) => (
    <DropdownMenuItem key={agent} className="gap-2.5 rounded-lg py-2" onSelect={() => onPick(agent)}>
      <AgentMark agentType={agent} className="size-4" />
      <span className="flex-1">{agentLabel(agent)}</span>
      <Check className="invisible size-3.5" aria-hidden="true" />
    </DropdownMenuItem>
  ));
}
