"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { ChevronRight, Clipboard, Download, Eye, MoreHorizontal, TerminalSquare } from "lucide-react";
import { Button } from "../ui/button";
import { ProcessedMessage } from "../processed-message";
import { toast } from "sonner";
import { taskToMarkdown } from "@/lib/task-actions";
import { groupConsecutiveTools } from "@/lib/activity-groups";
import { ToolCallCard, ToolCallGroup } from "./tool-call";
import { MessageItem } from "./message-item";
import { AgentMark } from "../agent-mark";
import { AgentType } from "../chat-provider";
import type { TaskSection, TaskStatus } from "@/lib/task-timeline";
import { getTaskActivity, hasStructuredTranscript, toSearchableTask } from "@/lib/task-timeline";
import { splitThinking } from "@/lib/thinking";
import { useMediaQuery } from "@/lib/use-media-query";
import { useThrottledValue } from "@/lib/use-throttled";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

export function TaskGroup({
  task,
  number,
  status,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  onStopTask,
  onSendRaw,
  searchQuery,
  searchResultIndex,
  isCurrentSearchResult,
  deferred = false,
  waitingForUser = false,
  agentType = "unknown",
}: {
  task: TaskSection;
  number: number;
  status: TaskStatus;
  onRetryMessage: (clientId: string) => Promise<boolean>;
  onEditMessage: (clientId: string, content: string) => void;
  onDismissMessage: (clientId: string) => void;
  onStopTask: () => void;
  onSendRaw?: (data: string) => void;
  searchQuery: string;
  searchResultIndex: number;
  isCurrentSearchResult: boolean;
  // Render only the prompt and a step count for now; the list renders the
  // full activity of older tasks progressively after the page has loaded.
  deferred?: boolean;
  // The agent is showing a prompt for this (the latest) task.
  waitingForUser?: boolean;
  // Names the agent's side of the task.
  agentType?: string;
}) {
  const agentName = AgentType[agentType as keyof typeof AgentType]?.displayName ?? "Agent";
  const statusMeta = waitingForUser
    ? {label: "Waiting for you", className: "text-state-needs"}
    : {
        queued: {label: "Queued", className: "text-muted-foreground"},
        running: {label: "Working", className: "text-state-working"},
        completed: {label: "Done", className: "text-state-ready"},
        failed: {label: "Failed", className: "text-state-fault"},
      }[status];
  const live = status === "running";
  const activity = useMemo(() => {
    if (deferred) return [];
    const raw = getTaskActivity(task, {live});
    // When rich activity provides real interleaving (text + tool entries),
    // tools are already positioned where they occurred in the conversation.
    // Only group consecutive tools in the fallback path (PTY-only), where
    // all tools are clustered together without interleaving context.
    const hasRichInterleaving =
      task.richActivity.some(item => item.type === "message" || item.type === "thinking") &&
      task.richActivity.some(item => item.type === "tool");
    return hasRichInterleaving ? raw : groupConsecutiveTools(raw);
  }, [task, deferred, live]);
  const structured = hasStructuredTranscript(task);
  // A long task shows its latest steps; earlier ones load on request.
  // Rendering hundreds of steps (Markdown, highlighted code) at once froze
  // slower devices. Search shows every step so matches can be found.
  const [showAllSteps, setShowAllSteps] = useState(false);
  const hiddenSteps = searchQuery || showAllSteps ? 0 : Math.max(0, activity.length - STEP_WINDOW);
  const visibleActivity = hiddenSteps > 0 ? activity.slice(hiddenSteps) : activity;
  const [terminalOpen, setTerminalOpen] = useState(false);
  const startedAt = task.prompt.time;
  const finishedAt = useMemo(() => {
    const times = [
      ...task.richActivity.map((item) =>
        item.type === "message" ? item.message.time : item.type === "tool" ? item.toolCall.resultTimestamp ?? item.toolCall.timestamp : item.timestamp),
      ...task.responses.map((message) => message.time),
    ].filter((time): time is string => Boolean(time));
    return times.sort().at(-1);
  }, [task]);
  // Converting a whole task to Markdown is costly for long tasks and only
  // needed for copy, export and preview, so do it on demand.
  const toMarkdown = () => taskToMarkdown(toSearchableTask(task), number);
  const [previewOpen, setPreviewOpen] = useState(false);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);

  useEffect(() => {
    if (status !== "running") {
      setElapsedSeconds(0);
      return;
    }
    const startedAt = task.prompt.time
      ? Date.parse(task.prompt.time)
      : Date.now();
    const update = () =>
      setElapsedSeconds(
        Math.max(0, Math.floor((Date.now() - startedAt) / 1000)),
      );
    update();
    const timer = window.setInterval(update, 1000);
    return () => window.clearInterval(timer);
  }, [status, task.prompt.time]);

  const duration = status === "running"
    ? formatClock(elapsedSeconds)
    : startedAt && finishedAt
      ? formatClock(Math.max(0, Math.round((Date.parse(finishedAt) - Date.parse(startedAt)) / 1000)))
      : "";

  const copyTask = async () => {
    try {
      await navigator.clipboard.writeText(toMarkdown());
      toast.success("Task copied");
    } catch {
      toast.error("Could not copy the task");
    }
  };

  const exportTask = () => {
    const url = URL.createObjectURL(
      new Blob([toMarkdown()], {type: "text/markdown;charset=utf-8"}),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = `agentapi-task-${number}.md`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
    toast.success(`Task ${number} Markdown downloaded`);
  };

  const startTime = startedAt ? formatClockTime(startedAt) : "";
  return (
    <section
      id={`task-${number}`}
      className={`task-entry relative scroll-mt-16 py-7 ${
        isCurrentSearchResult ? "rounded-lg ring-2 ring-ring" : ""
      }`}
      data-status={waitingForUser ? "waiting" : status}
      data-search-result={searchResultIndex}
    >
      {/* The time rail hangs in the left margin when there is room for it,
          so the text column itself stays centered. */}
      <div className="absolute right-full top-7 mr-6 hidden w-[3.25rem] pt-px text-right font-mono text-[11px] leading-5 tabular-nums text-muted-foreground lg:block">
        {startTime && <time dateTime={startedAt}>{startTime}</time>}
        {duration && <div className={status === "running" ? "text-state-working" : ""}>{duration}</div>}
      </div>
      <div className="min-w-0 min-[84rem]:flow-root">
        <header className="mb-2 flex min-h-7 items-center gap-2.5 font-mono text-[11px] uppercase tracking-[0.06em] text-muted-foreground">
          <span>Task {number}</span>
          {(startTime || duration) && (
            <span className="normal-case tracking-normal tabular-nums lg:hidden">
              {[startTime, duration].filter(Boolean).join(" · ")}
            </span>
          )}
          <span className={`flex items-center gap-1.5 font-sans text-xs normal-case tracking-normal ${statusMeta.className}`} role="status">
            <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />
            {statusMeta.label}
          </span>
          <div className="ml-auto flex items-center gap-1 font-sans normal-case tracking-normal">
            {status === "running" && (
              <Button type="button" size="sm" variant="outline" className="h-7 px-2.5 text-xs" onClick={onStopTask}>
                Stop
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button type="button" size="icon" variant="ghost" className="size-7 text-muted-foreground" title={`Task ${number} actions`}>
                  <MoreHorizontal />
                  <span className="sr-only">Task {number} actions</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={() => setPreviewOpen(true)}>
                  <Eye />
                  Preview Markdown
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => void copyTask()}>
                  <Clipboard />
                  Copy task and output
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={exportTask}>
                  <Download />
                  Export Markdown
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        <MessageItem
          message={task.prompt}
          onRetryMessage={onRetryMessage}
          onEditMessage={onEditMessage}
          onDismissMessage={onDismissMessage}
          searchQuery={searchQuery}
        />
        <div className="mt-5 space-y-4">
          {(deferred || activity.length > 0) && (
            // The agent's turn, so it doesn't read as part of your message.
            <div className="flex items-center gap-2 text-xs font-semibold text-foreground" data-agent-turn>
              <AgentMark agentType={agentType} />
              {agentName}
            </div>
          )}
          {deferred && (
            <p className="text-xs text-muted-foreground" role="status">
              {task.richActivity.length + task.responses.length} steps · loading…
            </p>
          )}
          {hiddenSteps > 0 && (
            <button
              type="button"
              onClick={() => setShowAllSteps(true)}
              className="flex items-center gap-1.5 rounded text-xs text-muted-foreground outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            >
              <ChevronRight className="size-3" />
              Show {hiddenSteps} earlier {hiddenSteps === 1 ? "step" : "steps"}
            </button>
          )}
          {visibleActivity.map((item, index) =>
            item.type === "message" ? (
              live && index === visibleActivity.length - 1 ? (
                // While a task runs, the last item is the live terminal
                // screen. With a transcript, only its tail is shown (the
                // rest is in the transcript). Either way it is redrawn at
                // most once a second and doesn't shrink, from the first
                // screen on, so the switch to a transcript doesn't remount it.
                <GrowOnly key={item.key} resetKey={structured ? "tail" : "screen"}>
                  <LiveTail message={structured ? withLastLines(item.message, 10) : item.message} searchQuery={searchQuery} />
                </GrowOnly>
              ) : (
                <MessageItem key={item.key} message={item.message} searchQuery={searchQuery} />
              )
            ) : item.type === "thinking" ? (
              <ThinkingNote key={item.key} content={item.content} />
            ) : item.type === "tool-group" ? (
              <ToolCallGroup key={item.key} toolCalls={item.toolCalls} searchQuery={searchQuery} onSendRaw={onSendRaw} />
            ) : (
              <ToolCallCard key={item.key} toolCall={item.toolCall} searchQuery={searchQuery} onSendRaw={onSendRaw} />
            ),
          )}
          {!deferred && structured && !live && task.responses.length > 0 && (
            <div>
              <button
                type="button"
                onClick={() => setTerminalOpen((open) => !open)}
                aria-expanded={terminalOpen}
                className="flex items-center gap-1.5 rounded text-xs text-muted-foreground outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              >
                <TerminalSquare className="size-3.5" />
                {terminalOpen ? "Hide terminal output" : "Terminal output"}
              </button>
              {terminalOpen && (
                <div className="mt-2 space-y-2">
                  {task.responses.map((message, index) => (
                    <MessageItem key={`terminal-${message.id ?? index}`} message={message} searchQuery={searchQuery} />
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
      <Dialog open={previewOpen} onOpenChange={setPreviewOpen}>
        <DialogContent className="flex max-h-[85dvh] w-full flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl">
          <DialogHeader className="border-b px-5 py-4 pr-12">
            <DialogTitle>Task {number} Markdown preview</DialogTitle>
            <DialogDescription>
              Rendered preview of the Markdown produced by copy and export.
            </DialogDescription>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
            <ProcessedMessage
              messageContent={previewOpen ? toMarkdown() : ""}
              isUser={false}
              renderMode="markdown"
            />
          </div>
          <DialogFooter className="gap-2 border-t px-5 py-3">
            <Button
              type="button"
              variant="ghost"
              onClick={() => void copyTask()}
            >
              <Clipboard />
              Copy
            </Button>
            <Button type="button" onClick={exportTask}>
              <Download />
              Download
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

// Thinking as a sidenote: on wide screens it sits in the right margin next to
// the step it explains; narrower screens show a one-line row that expands.
function ThinkingNote({content}: {content: string}) {
  // Wide enough for a 17rem note in the margin beside the centered column.
  const wide = useMediaQuery("(min-width: 84rem)");
  const [open, setOpen] = useState(false);
  const {title, body} = splitThinking(content);
  if (wide) {
    return (
      <aside className="float-right clear-right -mr-[17rem] mb-3 w-60 border-l pl-3.5 text-[12.5px] leading-[1.55] text-muted-foreground">
        <span className="mb-0.5 block font-mono text-[10.5px] uppercase tracking-[0.06em]">Thinking</span>
        <b className="block font-semibold text-foreground">{title}</b>
        {body && body !== title && (
          <p className={`mt-0.5 whitespace-pre-wrap ${open ? "" : "line-clamp-6"}`}>{body}</p>
        )}
        {body.length > 360 && (
          <button type="button" onClick={() => setOpen((value) => !value)} className="mt-1 text-[11px] underline-offset-2 hover:underline">
            {open ? "Less" : "More"}
          </button>
        )}
      </aside>
    );
  }
  return (
    <div className="text-xs text-muted-foreground">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        className="flex max-w-full items-center gap-1.5 rounded outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ChevronRight className={`size-3 shrink-0 transition-transform ${open ? "rotate-90" : ""}`} />
        <span className="truncate">Thinking · {title}</span>
      </button>
      {open && body && <p className="mt-1.5 whitespace-pre-wrap border-l pl-3.5 leading-[1.55]">{body}</p>}
    </div>
  );
}

const STEP_WINDOW = 40;

// The live screen tail, redrawn at most once a second. Agents animate
// parts of their screen several times a second (Claude Code blinks the
// bullet of a running tool); the latest screen still shows within a second.
function LiveTail({message, searchQuery}: {message: React.ComponentProps<typeof MessageItem>["message"]; searchQuery: string}) {
  const shown = useThrottledValue(message, 1000);
  return <MessageItem message={shown} searchQuery={searchQuery} />;
}

// Keeps the tallest height its content has had. The live screen tail
// grows and shrinks as the agent redraws; holding its height keeps the
// rest of the task from jumping up and down while it runs. A new resetKey
// drops the held height: the whole first screen (hundreds of rows) must
// not hold the space once only the tail of a transcript is shown.
function GrowOnly({resetKey, children}: {resetKey: string; children: React.ReactNode}) {
  const ref = useRef<HTMLDivElement>(null);
  const [held, setHeld] = useState({resetKey, height: 0});
  const minHeight = held.resetKey === resetKey ? held.height : 0;
  useEffect(() => {
    const element = ref.current;
    if (!element) return;
    const observer = new ResizeObserver(() => {
      setHeld((current) => {
        const height = Math.max(current.resetKey === resetKey ? current.height : 0, element.offsetHeight);
        return current.resetKey === resetKey && current.height === height ? current : {resetKey, height};
      });
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [resetKey]);
  return (
    <div ref={ref} style={minHeight ? {minHeight} : undefined} data-live-tail>
      {children}
    </div>
  );
}

function withLastLines<T extends {content: string}>(message: T, count: number): T {
  const lines = message.content.replace(/\s+$/, "").split("\n");
  return lines.length <= count ? message : {...message, content: lines.slice(-count).join("\n")};
}

function formatClock(totalSeconds: number) {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`
    : `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function formatClockTime(iso: string) {
  const date = new Date(iso);
  return Number.isNaN(date.getTime())
    ? ""
    : date.toLocaleTimeString([], {hour: "2-digit", minute: "2-digit", hour12: false});
}
