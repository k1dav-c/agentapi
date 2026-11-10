"use client";

import React, { useLayoutEffect, useMemo, useRef, useState } from "react";
import { formatDuration, formatTimeOfDay, secondsBetween } from "@/lib/clock";
import { useNow } from "@/lib/use-elapsed";
import { ChevronRight, Clipboard, Download, Eye, MoreHorizontal } from "lucide-react";
import { Button } from "../ui/button";
import { ProcessedMessage } from "../processed-message";
import { toast } from "sonner";
import { taskToMarkdown } from "@/lib/task-actions";
import { downloadFile } from "@/lib/download";
import { groupConsecutiveTools, splitAtThinking } from "@/lib/activity-groups";
import { ToolCallCard, ToolCallGroup } from "./tool-call";
import { MessageItem } from "./message-item";
import { AgentMark } from "../agent-mark";
import {AgentType} from "@/lib/agents";
import type { TaskSection, TaskStatus } from "@/lib/task-timeline";
import { TASK_STATUS_META, getTaskActivity, toSearchableTask } from "@/lib/task-timeline";
import { splitThinking } from "@/lib/thinking";
import { useMediaQuery } from "@/lib/use-media-query";
import { copyText } from "@/lib/clipboard";
import { useDialogFromMenu } from "@/lib/menu-dialog";
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

// A task: memoized, so that while the agent streams into the latest task
// the others don't render again.
export const TaskGroup = React.memo(function TaskGroup({
  task,
  number,
  status,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  onStopTask,
  searchQuery,
  searchResultIndex,
  isCurrentSearchResult,
  deferred = false,
  waitingForUser = false,
  agentType = "unknown",
  promptAuthor,
  cost,
  costDetails,
  statusDetail,
}: {
  task: TaskSection;
  number: number;
  status: TaskStatus;
  onRetryMessage: (clientId: string) => Promise<boolean>;
  onEditMessage: (clientId: string, content: string) => void;
  onDismissMessage: (clientId: string) => void;
  onStopTask?: () => void;
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
  // Who gave the task when it isn't you (a subagent's, the agent).
  promptAuthor?: string;
  // What the task's model calls cost at list prices ("$1.23"), and the
  // tokens behind it, when known.
  cost?: string;
  costDetails?: string;
  // How the task ended, beside its status: a failed task's first error, a
  // limited one's limit.
  statusDetail?: string;
}) {
  const agentName = AgentType[agentType as keyof typeof AgentType]?.displayName ?? "Agent";
  const statusMeta = waitingForUser ? {label: "Waiting for you", className: "text-state-needs"} : TASK_STATUS_META[status];
  const live = status === "running";
  const activity = useMemo(() => {
    if (deferred) return [];
    const raw = getTaskActivity(task, {transcriptOnly: true});
    // When rich activity provides real interleaving (text + tool entries),
    // tools are already positioned where they occurred in the conversation.
    // Only group consecutive tools in the fallback path, where
    // all tools are clustered together without interleaving context.
    const hasRichInterleaving =
      task.richActivity.some(item => item.type === "message" || item.type === "thinking") &&
      task.richActivity.some(item => item.type === "tool");
    return hasRichInterleaving ? raw : groupConsecutiveTools(raw);
  }, [task, deferred]);
  // A long task shows its latest steps; earlier ones load on request.
  // Rendering hundreds of steps (Markdown, highlighted code) at once froze
  // slower devices. Search shows every step so matches can be found.
  const [showAllSteps, setShowAllSteps] = useState(false);
  const hiddenSteps = searchQuery || showAllSteps ? 0 : Math.max(0, activity.length - STEP_WINDOW);
  const visibleActivity = hiddenSteps > 0 ? activity.slice(hiddenSteps) : activity;
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
  const previewFromMenu = useDialogFromMenu(() => setPreviewOpen(true));
  const hasDuration = live || Boolean(startedAt && finishedAt);
  const duration = hasDuration && <TaskDuration running={live} startedAt={startedAt} finishedAt={finishedAt} />;

  const copyTask = async () => {
    try {
      await copyText(toMarkdown());
      toast.success("Task copied");
    } catch {
      toast.error("Could not copy the task");
    }
  };

  const exportTask = () => {
    downloadFile(`agentapi-task-${number}.md`, toMarkdown(), "text/markdown;charset=utf-8");
    toast.success(`Task ${number} Markdown downloaded`);
  };

  const startTime = startedAt ? formatTimeOfDay(startedAt) : "";
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
        {duration && <div className={live ? "text-state-working" : ""}>{duration}</div>}
        {cost && (
          <div title={costDetails} data-task-cost>
            {cost}
          </div>
        )}
      </div>
      <div className="min-w-0 min-[84rem]:flow-root">
        <header className="mb-2 flex min-h-7 items-center gap-2.5 font-mono text-[11px] uppercase tracking-[0.06em] text-muted-foreground">
          <span>Task {number}</span>
          {(startTime || duration || cost) && (
            <span className="normal-case tracking-normal tabular-nums lg:hidden">
              {[startTime, duration, cost && <span key="cost" title={costDetails}>{cost}</span>]
                .filter(Boolean)
                .map((part, index) => (
                  <React.Fragment key={index}>
                    {index > 0 && " · "}
                    {part}
                  </React.Fragment>
                ))}
            </span>
          )}
          <span className={`flex items-center gap-1.5 font-sans text-xs normal-case tracking-normal ${statusMeta.className}`} role="status" title={statusDetail}>
            <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />
            {statusMeta.label}
          </span>
          <div className="ml-auto flex items-center gap-1 font-sans normal-case tracking-normal">
            {status === "running" && onStopTask && (
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
              <DropdownMenuContent align="end" onCloseAutoFocus={previewFromMenu.onCloseAutoFocus}>
                <DropdownMenuItem onSelect={previewFromMenu.onSelect}>
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
          author={promptAuthor}
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
          {splitAtThinking(visibleActivity).map((part) => {
            const notes = part.filter(({item}) => item.type === "thinking");
            const steps = part.filter(({item}) => item.type !== "thinking");
            return (
              // On a wide screen thinking sits beside the steps it explains, in a
              // second column reaching into the right margin: a long note makes
              // room there instead of pushing the notes after it down, and the
              // steps keep their width; beside steps, the notes take no more
              // height than they do (MarginNotes). Narrower screens show it
              // inline.
              <div
                key={part[0].item.key}
                className="space-y-4 min-[84rem]:-mr-[17rem] min-[84rem]:grid min-[84rem]:grid-cols-[minmax(0,1fr)_15rem] min-[84rem]:gap-x-8 min-[84rem]:space-y-0"
              >
                {notes.length > 0 && (
                  <MarginNotes besideSteps={steps.length > 0}>
                    {notes.map(({item}) => item.type === "thinking" && <ThinkingNote key={item.key} content={item.content} />)}
                  </MarginNotes>
                )}
                {steps.length > 0 && (
                  <div className="min-w-0 space-y-4 min-[84rem]:col-start-1 min-[84rem]:row-start-1">
                    {steps.map(({item}) =>
                      item.type === "message" ? (
                        <MessageItem key={item.key} message={item.message} searchQuery={searchQuery} />
                      ) : item.type === "prompt" ? (
                        <MessageItem key={item.key} message={item.message} searchQuery={searchQuery} steered />
                      ) : item.type === "thinking" ? (
                        <ThinkingNote key={item.key} content={item.content} />
                      ) : item.type === "tool-group" ? (
                        <ToolCallGroup key={item.key} toolCalls={item.toolCalls} searchQuery={searchQuery} />
                      ) : (
                        <ToolCallCard key={item.key} toolCall={item.toolCall} searchQuery={searchQuery} />
                      )
                    )}
                  </div>
                )}
              </div>
            );
          })}
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
});

// MarginNotes is the column of thinking notes beside a part's steps on a wide
// screen. Beside steps it is as tall as they are, not taller, so that long
// thinking beside one short step doesn't open a gap in the transcript: what
// doesn't fit fades out above Show all, which lets the column grow. Notes
// with no steps beside them, and narrower screens, show in full.
function MarginNotes({besideSteps, children}: {besideSteps: boolean; children: React.ReactNode}) {
  const wide = useMediaQuery("(min-width: 84rem)");
  const [open, setOpen] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const clamped = wide && besideSteps && !open;
  useLayoutEffect(() => {
    const el = box.current;
    if (!clamped || !el) {
      setOverflows(false);
      return;
    }
    const measure = () => setOverflows(el.scrollHeight > el.clientHeight + 1);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    if (el.firstElementChild) observer.observe(el.firstElementChild);
    return () => observer.disconnect();
  }, [clamped]);
  return (
    <div
      className={`relative min-[84rem]:col-start-2 min-[84rem]:row-start-1 ${
        // Out of the row's sizing (no height of its own), then as tall as the
        // row, and tall enough for a note's title beside one short step.
        clamped ? "min-[84rem]:h-0 min-[84rem]:min-h-[max(100%,5rem)]" : ""
      }`}
    >
      <div ref={box} className={clamped ? "h-full overflow-hidden" : undefined}>
        <div className="space-y-3">{children}</div>
      </div>
      {wide && besideSteps && (overflows || open) && (
        <div className={clamped ? "absolute inset-x-0 bottom-0 bg-gradient-to-t from-background from-40% to-transparent pt-8" : "mt-1"}>
          <button
            type="button"
            onClick={() => setOpen((value) => !value)}
            aria-expanded={open}
            className="ml-3.5 text-[11px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
          >
            {open ? "Less" : "Show all"}
          </button>
        </div>
      )}
    </div>
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
      <aside className="border-l pl-3.5 text-[12.5px] leading-[1.55] text-muted-foreground">
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

// How long a task took, or has taken so far while it runs. A component of
// its own, so that the clock of the running task renders every second
// rather than the whole task.
function TaskDuration({running, startedAt, finishedAt}: {running: boolean; startedAt?: string; finishedAt?: string}) {
  const now = useNow(running ? 1000 : null);
  // A task whose prompt has no time counts from when it showed.
  const [shownAt] = useState(() => Date.now());
  const start = startedAt ? Date.parse(startedAt) : shownAt;
  if (running) return formatDuration(secondsBetween(start, now));
  if (!startedAt || !finishedAt) return null;
  return formatDuration(Math.max(0, Math.round((Date.parse(finishedAt) - start) / 1000)));
}
