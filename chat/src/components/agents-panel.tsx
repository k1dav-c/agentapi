"use client";

import {useEffect, useMemo, useState} from "react";
import {Bot, CheckCircle2, CircleAlert, CircleSlash, LoaderCircle} from "lucide-react";
import {
  agentName,
  countRunningAgents,
  formatElapsed,
  formatTokens,
  sortAgentsForDisplay,
  type SubAgent,
} from "@/lib/agents";
import {useChat} from "./chat-provider";
import {Button} from "./ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "./ui/dialog";

// Lists the sub-agents the agent spawned (Codex multi-agent). Hidden until
// the first sub-agent appears. Alt+↑ toggles it outside Terminal mode, which
// keeps Alt+↑ for the agent itself.
export function AgentsPanel() {
  const {agents} = useChat();
  const [open, setOpen] = useState(false);
  const running = countRunningAgents(agents);
  const sorted = useMemo(() => sortAgentsForDisplay(agents), [agents]);
  const now = useNow(open && running > 0);

  useEffect(() => {
    if (agents.length === 0) return;
    const handler = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.key === "ArrowUp" && e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
        e.preventDefault();
        setOpen((value) => !value);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [agents.length]);

  if (agents.length === 0) return null;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-9"
          title="Sub-agents (Alt+↑)"
        >
          {running > 0 ? <LoaderCircle className="animate-spin" /> : <Bot />}
          <span className="hidden sm:inline">Agents</span>
          <span className="rounded-full bg-muted px-1.5 text-[11px] font-semibold tabular-nums">
            {running > 0 ? `${running}/${agents.length}` : agents.length}
          </span>
          <span className="sr-only">
            {running} of {agents.length} sub-agents running
          </span>
        </Button>
      </DialogTrigger>
      <DialogContent className="left-auto right-0 top-0 flex h-dvh max-h-dvh w-full max-w-md translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none p-0 sm:rounded-none">
        <DialogHeader className="border-b px-5 py-4 pr-12">
          <DialogTitle>Agents</DialogTitle>
          <DialogDescription>
            {running > 0
              ? `${running} running · ${agents.length} total`
              : `${agents.length} sub-agents · none running`}
          </DialogDescription>
        </DialogHeader>
        <ul className="min-h-0 flex-1 space-y-2 overflow-y-auto overscroll-contain p-4">
          {sorted.map((agent) => (
            <AgentRow key={agent.thread_id} agent={agent} now={now} />
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}

function AgentRow({agent, now}: {agent: SubAgent; now: number}) {
  const isRunning = agent.status === "running";
  const elapsed = isRunning
    ? formatElapsed(agent.started_at, now)
    : formatElapsed(agent.started_at, Date.parse(agent.updated_at));
  const details = [elapsed, formatTokens(agent.total_tokens)].filter(Boolean).join(" · ");

  return (
    <li
      className="rounded-xl border p-3"
      style={{marginLeft: `${Math.max(0, agent.depth - 1) * 1}rem`}}
    >
      <div className="flex items-center gap-2">
        <StatusIcon status={agent.status} />
        <span className="min-w-0 flex-1 truncate text-sm font-medium" title={agent.path}>
          {agentName(agent)}
        </span>
        {agent.nickname && (
          <span className="shrink-0 text-xs text-muted-foreground">{agent.nickname}</span>
        )}
      </div>
      <div className="mt-1 flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span className="truncate font-mono" title={agent.path}>{agent.path}</span>
        <span className="shrink-0 tabular-nums">{details}</span>
      </div>
      {isRunning && agent.activity && (
        <p className="mt-2 truncate rounded-md bg-muted/50 px-2 py-1 font-mono text-xs" title={agent.activity}>
          {agent.activity}
        </p>
      )}
      {agent.last_message && (
        <p className="mt-2 line-clamp-3 whitespace-pre-wrap text-xs text-muted-foreground">
          {agent.last_message}
        </p>
      )}
    </li>
  );
}

function StatusIcon({status}: {status: SubAgent["status"]}) {
  switch (status) {
    case "running":
      return <LoaderCircle aria-label="Running" className="size-3.5 shrink-0 animate-spin text-muted-foreground" />;
    case "completed":
      return <CheckCircle2 aria-label="Completed" className="size-3.5 shrink-0 text-status-success" />;
    case "interrupted":
      return <CircleSlash aria-label="Interrupted" className="size-3.5 shrink-0 text-status-warning" />;
    case "failed":
      return <CircleAlert aria-label="Failed" className="size-3.5 shrink-0 text-destructive" />;
  }
}

// Current time, refreshed every second while ticking is true.
function useNow(ticking: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [ticking]);
  return now;
}
