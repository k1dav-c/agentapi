"use client";

import {useEffect, useMemo, useRef, useState} from "react";
import {ChevronLeft, ChevronRight, LoaderCircle, Workflow} from "lucide-react";
import {useChatActions, useChatSelector} from "./chat-provider";
import {Button} from "./ui/button";
import {Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle} from "./ui/dialog";
import {TaskGroup} from "./message-list/task-group";
import type {Subagent, SubagentRun} from "@/lib/chat-api";
import type {TaskStatus} from "@/lib/task-timeline";
import {getErrorMessage} from "@/lib/error-utils";
import {OPEN_LINEAGE_EVENT, subagentTask} from "@/lib/subagents";
import {cn} from "@/lib/utils";
import {agentDisplayName} from "@/lib/agents";
import {sameValue} from "@/lib/agent-status";

// How often an open panel reads the subagents again, and a running
// subagent's steps: they come from the subagents' logs, which grow as they
// work.
const LIST_REFRESH_MS = 3000;
const RUN_REFRESH_MS = 2000;

const STATUS: Record<Subagent["status"], {label: string; className: string}> = {
  running: {label: "Working", className: "text-state-working"},
  completed: {label: "Done", className: "text-state-ready"},
  failed: {label: "Failed", className: "text-state-fault"},
  unknown: {label: "", className: "text-muted-foreground"},
};

const TASK_STATUS: Record<Subagent["status"], TaskStatus> = {
  running: "running",
  completed: "completed",
  failed: "failed",
  unknown: "completed",
};

const noop = () => undefined;

// The subagents the agent ran in its session (Claude Code's Agent tool), in
// a panel: each with what it was asked, whether it still works and for how
// long, and what it did, step by step, as the transcript shows a task. The
// header button counts them; a subagent's tool call opens the panel on it.
export function Lineage() {
  const subagentsShown = useChatSelector((chat) => chat.features.subagents);
  const agentType = useChatSelector((chat) => chat.agentType);
  // Counted as tool calls change (useTranscript): the button renders again
  // when a subagent starts or ends, not whenever a message streams.
  const counts = useChatSelector((chat) => chat.subagents, sameValue);
  const {listSubagents, getSubagent} = useChatActions();
  const [open, setOpen] = useState(false);
  const [subagents, setSubagents] = useState<Subagent[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [run, setRun] = useState<SubagentRun | null>(null);
  const [error, setError] = useState("");
  // The tool call whose subagent to show once the list has it.
  const wanted = useRef<string | undefined>(undefined);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const onOpen = (event: Event) => {
      wanted.current = (event as CustomEvent<string | undefined>).detail;
      setSelected(null);
      setOpen(true);
    };
    window.addEventListener(OPEN_LINEAGE_EVENT, onOpen);
    return () => window.removeEventListener(OPEN_LINEAGE_EVENT, onOpen);
  }, []);

  // The list, while the panel is open.
  useEffect(() => {
    if (!open) return;
    let active = true;
    const load = async () => {
      try {
        const list = await listSubagents();
        if (!active) return;
        setSubagents(list);
        setError("");
        setNow(Date.now());
        if (wanted.current) {
          const match = list.find((subagent) => subagent.parent_tool_use_id === wanted.current);
          if (match) {
            setSelected(match.id);
            wanted.current = undefined;
          }
        }
      } catch (failure) {
        if (active) setError(getErrorMessage(failure));
      }
    };
    void load();
    const timer = setInterval(() => void load(), LIST_REFRESH_MS);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [open, listSubagents]);

  // The selected subagent's steps, again while it works.
  const selectedStatus = subagents?.find((subagent) => subagent.id === selected)?.status;
  useEffect(() => {
    if (!open || !selected) {
      setRun(null);
      return;
    }
    let active = true;
    const load = async () => {
      try {
        const next = await getSubagent(selected);
        if (active) setRun(next);
      } catch (failure) {
        if (active) setError(getErrorMessage(failure));
      }
    };
    void load();
    if (selectedStatus !== "running") return () => void (active = false);
    const timer = setInterval(() => void load(), RUN_REFRESH_MS);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [open, selected, selectedStatus, getSubagent]);

  const task = useMemo(() => (run && run.subagent.id === selected ? subagentTask(run) : null), [run, selected]);

  if (!subagentsShown || counts.total === 0) return null;
  const current = subagents?.find((subagent) => subagent.id === selected);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) wanted.current = undefined;
      }}
    >
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-8 gap-1 rounded-full px-2 text-xs text-muted-foreground"
        title={`Subagents: ${counts.total}${counts.running ? `, ${counts.running} working` : ""}`}
        onClick={() => {
          wanted.current = undefined;
          setSelected(null);
          setOpen(true);
        }}
        data-lineage-button
      >
        <Workflow className="size-4" />
        <span className="tabular-nums">{counts.running ? `${counts.running}/${counts.total}` : counts.total}</span>
        {counts.running > 0 && <span aria-hidden="true" className="state-led size-1.5 rounded-full bg-state-working" />}
        <span className="sr-only">Open the subagents</span>
      </Button>
      <DialogContent className="left-auto right-0 top-0 flex h-dvh max-h-dvh w-full max-w-3xl translate-x-0 translate-y-0 flex-col gap-0 overflow-hidden rounded-none p-0 sm:rounded-none">
        <DialogHeader className="border-b px-5 py-4 pr-12">
          <DialogTitle className="flex min-w-0 items-center gap-1.5">
            {current ? (
              <>
                <button
                  type="button"
                  onClick={() => setSelected(null)}
                  className="flex shrink-0 items-center gap-0.5 rounded text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <ChevronLeft className="size-4" />
                  Lineage
                </button>
                <span className="text-muted-foreground">/</span>
                <span className="min-w-0 truncate">{label(current)}</span>
              </>
            ) : (
              "Lineage"
            )}
          </DialogTitle>
          <DialogDescription>
            {current
              ? `${STATUS[current.status].label || "Subagent"} · ${elapsed(current, now)}${current.agent_type ? ` · ${current.agent_type}` : ""}`
              : "The subagents the agent ran in this session, and what each did."}
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-4" data-lineage>
          {error && <p role="alert" className="mb-3 text-sm text-destructive">{error}</p>}
          {current ? (
            task ? (
              <TaskGroup
                task={task}
                number={(subagents?.indexOf(current) ?? 0) + 1}
                status={TASK_STATUS[current.status]}
                onRetryMessage={async () => false}
                onEditMessage={noop}
                onDismissMessage={noop}
                searchQuery=""
                searchResultIndex={-1}
                isCurrentSearchResult={false}
                agentType={agentType}
                promptAuthor={`From ${agentDisplayName(agentType, "the agent")}`}
              />
            ) : (
              <Loading text="Reading the subagent's steps…" />
            )
          ) : subagents === null ? (
            <Loading text="Reading the subagents…" />
          ) : subagents.length === 0 ? (
            <p className="rounded-xl border border-dashed p-6 text-center text-sm text-muted-foreground">
              The subagents&apos; logs aren&apos;t there yet. They show once the agent writes them.
            </p>
          ) : (
            <ul className="space-y-2">
              {subagents.map((subagent) => (
                <li key={subagent.id}>
                  <button
                    type="button"
                    onClick={() => setSelected(subagent.id)}
                    className="flex w-full items-center gap-3 rounded-xl border p-3 text-left outline-none transition hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring"
                    data-subagent={subagent.id}
                  >
                    <span
                      aria-hidden="true"
                      className={cn(
                        "size-2 shrink-0 rounded-full",
                        subagent.status === "running" ? "state-led bg-state-working" : subagent.status === "failed" ? "bg-state-fault" : subagent.status === "completed" ? "bg-state-ready" : "bg-muted-foreground/40",
                      )}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{label(subagent)}</span>
                      <span className="block text-[11px] text-muted-foreground">
                        {[subagent.agent_type, elapsed(subagent, now), subagent.id.slice(0, 8)].filter(Boolean).join(" · ")}
                      </span>
                    </span>
                    {STATUS[subagent.status].label && (
                      <span className={cn("shrink-0 text-xs font-medium", STATUS[subagent.status].className)}>{STATUS[subagent.status].label}</span>
                    )}
                    <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function Loading({text}: {text: string}) {
  return (
    <p className="flex items-center gap-2 text-sm text-muted-foreground">
      <LoaderCircle className="size-4 animate-spin" /> {text}
    </p>
  );
}

// What a subagent is called: its description, else its id.
function label(subagent: Subagent): string {
  return subagent.description || `Subagent ${subagent.id.slice(0, 8)}`;
}

// How long a subagent ran: until its log last changed, or until now while it
// works.
function elapsed(subagent: Subagent, now: number): string {
  const start = Date.parse(subagent.started_at);
  const end = subagent.status === "running" ? now : Date.parse(subagent.updated_at);
  const seconds = Math.max(0, Math.round((end - start) / 1000));
  if (!Number.isFinite(seconds)) return "";
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return minutes < 60 ? `${minutes}m ${seconds % 60}s` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}
