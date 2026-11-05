"use client";

import {AgentMark} from "@/components/agent-mark";
import {displayName, instanceState, sameMachine, stateLabel, type HubInstance, type InstanceState} from "@/lib/hub";
import type {MachineAgent} from "@/lib/hub-frame";
import {cn} from "@/lib/utils";
import {agentName} from "./instance-row";

// The dot on an agent's tab, in its state's color: none while it is idle
// or connecting, which leaves the ones to look at standing out.
const dotTone: Partial<Record<InstanceState, string>> = {
  "needs-you": "bg-state-needs",
  working: "bg-state-working animate-pulse",
  unread: "bg-state-ready",
  muted: "bg-muted-foreground/50",
  unreachable: "bg-state-fault",
  stopped: "bg-muted-foreground",
  unknown: "bg-muted-foreground/50",
};

// AgentTabMark is an agent's mark on its tab, with its state's dot at the
// corner.
export function AgentTabMark({agentType, state, className}: {agentType?: string; state: InstanceState; className?: string}) {
  const dot = dotTone[state];
  return (
    <span className={cn("relative inline-flex shrink-0", className)} data-tab-state={state}>
      <AgentMark agentType={agentType || "custom"} className="size-4" />
      {dot && <span aria-hidden="true" className={cn("absolute -right-1 -bottom-0.5 size-2 rounded-full ring-2 ring-background", dot)} />}
    </span>
  );
}

// shortAgent names an agent on a tab: Claude Code as Claude.
export function shortAgent(agentType: string | undefined, agent: string): string {
  return agentType === "claude" ? "Claude" : agent;
}

// machineAgent is an instance as the bar's switch shows it.
export function machineAgent(instance: HubInstance): MachineAgent {
  return {
    id: instance.id,
    name: displayName(instance),
    state: instanceState(instance),
    label: stateLabel(instance),
    agentType: instance.agent_type || undefined,
    agent: agentName(instance) || undefined,
  };
}

// machineAgents are the agents of instance's machine for the bar's switch:
// none when it is the only one there or names no machine.
export function machineAgents(instances: Iterable<HubInstance>, instance: HubInstance): MachineAgent[] {
  return sameMachine(instances, instance).map(machineAgent);
}

// MachineSwitch is the open agent's machine in the bar above its chat: a
// button per agent there, the open one marked, which opens another in its
// place. Marks only, so a phone's bar keeps its title.
export function MachineSwitch({agents, current, onOpen}: {agents: MachineAgent[]; current: string; onOpen: (id: string) => void}) {
  if (agents.length < 2) return null;
  return (
    <div role="group" aria-label="Agents on this machine" className="flex shrink-0 items-center gap-0.5 rounded-lg border bg-muted/50 p-0.5" data-machine-switch>
      {agents.map((agent) => {
        const open = agent.id === current;
        const what = `${agent.agent || "Agent"}, ${agent.name}: ${agent.label}`;
        return (
          <button
            key={agent.id}
            type="button"
            aria-current={open ? "true" : undefined}
            aria-label={open ? `${what} (open)` : `Open ${what}`}
            title={what}
            onClick={() => {
              if (!open) onOpen(agent.id);
            }}
            className={cn(
              "flex size-7 items-center justify-center rounded-md outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50 sm:size-8",
              open ? "bg-background shadow-xs ring-1 ring-border dark:bg-accent" : "opacity-70 hover:bg-accent/60 hover:opacity-100",
            )}
          >
            <AgentTabMark agentType={agent.agentType} state={agent.state} />
          </button>
        );
      })}
    </div>
  );
}
