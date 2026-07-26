"use client";

import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  CircleAlert,
  Check,
  CheckCircle2,
  Clipboard,
  Clock3,
  Code2,
  Download,
  Eye,
  LoaderCircle,
  MessageSquarePlus,
  MoreHorizontal,
  Pencil,
  RefreshCw,
  Search,
  Sparkles,
  TerminalSquare,
  User,
  Wrench,
  X,
} from "lucide-react";
import { Button } from "./ui/button";
import type {
  AgentType,
  DraftMessage,
  Message,
  RichMessage,
  ServerStatus,
} from "./chat-provider";
import {ProcessedMessage} from "./processed-message";
import {toast} from "sonner";
import {taskMatchesQuery, taskToMarkdown} from "@/lib/task-actions";
import {groupConsecutiveTools} from "@/lib/activity-groups";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";

interface MessageListProps {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  serverStatus: ServerStatus;
  agentType: AgentType;
  onSelectPrompt?: (prompt: string) => void;
  onRetryMessage: (clientId: string) => Promise<boolean>;
  onEditMessage: (clientId: string, content: string) => void;
  onDismissMessage: (clientId: string) => void;
  onRunTask: (content: string) => void;
  onStopTask: () => void;
}

interface ToolCall {
  id: string;
  name: string;
  input?: unknown;
  result?: string;
  status?: "running" | "completed" | "failed";
  isError?: boolean;
  timestamp: string;
}

interface TaskSection {
  key: string;
  prompt: Message | DraftMessage;
  responses: (Message | DraftMessage)[];
  toolCalls: ToolCall[];
  richActivity: TaskActivity[];
}

type TaskActivity =
  | {
      type: "message";
      key: string;
      message: Message | DraftMessage;
    }
  | {
      type: "tool";
      key: string;
      toolCall: ToolCall;
    };

type TaskStatus = "queued" | "running" | "completed" | "failed";
type TaskFilter = "all" | TaskStatus | "tool-error";

function getTaskStatus(
  task: TaskSection,
  index: number,
  taskCount: number,
  serverStatus: ServerStatus,
): TaskStatus {
  if (
    task.prompt.id === undefined &&
    (task.prompt as DraftMessage).deliveryStatus === "failed"
  ) {
    return "failed";
  }
  if (task.prompt.id === undefined) {
    return serverStatus === "running" ? "queued" : "running";
  }
  if (task.toolCalls.some((tool) => tool.isError)) return "failed";
  if (index === taskCount - 1 && serverStatus === "running") return "running";
  return "completed";
}

export default function MessageList({
  messages,
  richMessages,
  serverStatus,
  agentType,
  onSelectPrompt,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  onRunTask,
  onStopTask,
}: MessageListProps) {
  const [scrollArea, setScrollArea] = useState<HTMLDivElement | null>(null);
  const [showScrollButton, setShowScrollButton] = useState(false);
  const [unreadCount, setUnreadCount] = useState(0);
  const [canScrollToPreviousUser, setCanScrollToPreviousUser] =
    useState(false);
  const [canScrollToNextUser, setCanScrollToNextUser] = useState(false);
  const [showAllTasks, setShowAllTasks] = useState(false);
  const [taskQuery, setTaskQuery] = useState("");
  const [taskFilter, setTaskFilter] = useState<TaskFilter>("all");
  const [currentSearchResult, setCurrentSearchResult] = useState(0);
  const searchInputRef = useRef<HTMLInputElement>(null);
  const isAtBottomRef = useRef(true);
  const lastScrollHeightRef = useRef(0);
  const userMessageCount = messages.filter(
    (message) => message.role === "user",
  ).length;
  const toolCalls = useMemo(() => collectToolCalls(richMessages), [richMessages]);
  const timeline = useMemo(() => {
    const tasks: TaskSection[] = [];
    const prelude: (Message | DraftMessage)[] = [];

    for (const message of messages) {
      if (message.role === "user") {
        tasks.push({
          key: `task-${message.id ?? `draft-${tasks.length}`}`,
          prompt: message,
          responses: [],
          toolCalls: [],
          richActivity: [],
        });
      } else if (tasks.length > 0) {
        tasks.at(-1)!.responses.push(message);
      } else {
        prelude.push(message);
      }
    }

    for (const toolCall of toolCalls) {
      const target = findTaskAtTime(tasks, toolCall.timestamp);
      target?.toolCalls.push(toolCall);
    }

    const callsByID = new Map(toolCalls.map((toolCall) => [toolCall.id, toolCall]));
    for (const message of richMessages) {
      if (message.role !== "assistant") continue;
      const target = findTaskAtTime(tasks, message.timestamp);
      if (!target) continue;

      message.content.forEach((block, index) => {
        if (block.type === "text" && block.text) {
          target.richActivity.push({
            type: "message",
            key: `rich-message-${message.message_id}-${index}`,
            message: {
              id: -1,
              role: "assistant",
              content: block.text,
              time: message.timestamp,
            },
          });
        }
        if (block.type === "tool_use" && block.tool_use_id) {
          const toolCall = callsByID.get(block.tool_use_id);
          if (toolCall) {
            target.richActivity.push({
              type: "tool",
              key: `rich-tool-${block.tool_use_id}`,
              toolCall,
            });
          }
        }
      });
    }

    return {prelude, tasks};
  }, [messages, richMessages, toolCalls]);
  const filteredTasks = useMemo(
    () =>
      timeline.tasks
        .map((task, index) => ({
          task,
          index,
          status: getTaskStatus(
            task,
            index,
            timeline.tasks.length,
            serverStatus,
          ),
          matchCount: countTaskMatches(task, taskQuery),
        }))
        .filter(({task, status, matchCount}) => {
          const matchesFilter =
            taskFilter === "all" ||
            (taskFilter === "tool-error"
              ? task.toolCalls.some((tool) => tool.isError)
              : status === taskFilter);
          return (
            matchesFilter &&
            (taskQuery.trim() === "" ||
              matchCount > 0 ||
              taskMatchesQuery(toSearchableTask(task), taskQuery))
          );
        }),
    [serverStatus, taskFilter, taskQuery, timeline.tasks],
  );
  const filtersActive = taskQuery.trim() !== "" || taskFilter !== "all";
  const totalMatchCount = filteredTasks.reduce(
    (total, task) => total + task.matchCount,
    0,
  );
  const hiddenTaskCount =
    !filtersActive && !showAllTasks && filteredTasks.length > 8
      ? filteredTasks.length - 8
      : 0;
  const visibleTasks =
    hiddenTaskCount > 0
      ? filteredTasks.slice(hiddenTaskCount)
      : filteredTasks;
  const contentSignature = useMemo(
    () =>
      [
        ...timeline.prelude.map((message, index) =>
          `prelude-${message.id ?? index}:${message.content.length}`),
        ...timeline.tasks.map((task) =>
          `${task.key}:${task.prompt.content.length}:${task.responses
            .map((message) => message.content.length)
            .join(",")}:${task.toolCalls
            .map((tool) => `${tool.id}:${tool.result?.length ?? -1}`)
            .join(",")}`),
      ].join("|"),
    [timeline],
  );

  useEffect(() => {
    setCurrentSearchResult(0);
  }, [taskFilter, taskQuery]);

  const navigateSearchResults = useCallback(
    (direction: -1 | 1) => {
      if (!scrollArea || filteredTasks.length === 0) return;
      const next =
        (currentSearchResult + direction + filteredTasks.length) %
        filteredTasks.length;
      setCurrentSearchResult(next);
      scrollArea
        .querySelector<HTMLElement>(`[data-search-result="${next}"]`)
        ?.scrollIntoView({behavior: "smooth", block: "center"});
    },
    [currentSearchResult, filteredTasks.length, scrollArea],
  );

  useEffect(() => {
    const handleGlobalSearchShortcut = (event: globalThis.KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "f") {
        event.preventDefault();
        searchInputRef.current?.focus();
        searchInputRef.current?.select();
      } else if (
        event.key === "Escape" &&
        document.activeElement === searchInputRef.current
      ) {
        setTaskQuery("");
        searchInputRef.current?.blur();
      }
    };
    window.addEventListener("keydown", handleGlobalSearchShortcut);
    return () =>
      window.removeEventListener("keydown", handleGlobalSearchShortcut);
  }, []);

  const scrollToBottom = useCallback(
    (behavior: ScrollBehavior = "smooth") => {
      scrollArea?.scrollTo({ top: scrollArea.scrollHeight, behavior });
      isAtBottomRef.current = true;
      setShowScrollButton(false);
      setUnreadCount(0);
    },
    [scrollArea],
  );

  const scrollToPreviousUserMessage = useCallback(() => {
    if (!scrollArea) return;

    const targetTop = getPreviousUserMessageTop(scrollArea);
    if (targetTop === undefined) return;
    scrollArea.scrollTo({
      top: Math.max(0, targetTop - 16),
      behavior: "smooth",
    });
  }, [scrollArea]);

  const scrollToNextUserMessage = useCallback(() => {
    if (!scrollArea) return;
    const targetTop = getNextUserMessageTop(scrollArea);
    if (targetTop === undefined) return;
    scrollArea.scrollTo({top: Math.max(0, targetTop - 16), behavior: "smooth"});
  }, [scrollArea]);

  useEffect(() => {
    if (!scrollArea) return;

    const handleScroll = () => {
      const { scrollTop, scrollHeight, clientHeight } = scrollArea;
      const atBottom = scrollTop + clientHeight >= scrollHeight - 32;
      isAtBottomRef.current = atBottom;
      setShowScrollButton(!atBottom);
      if (atBottom) setUnreadCount(0);
      setCanScrollToPreviousUser(
        getPreviousUserMessageTop(scrollArea) !== undefined,
      );
      setCanScrollToNextUser(getNextUserMessageTop(scrollArea) !== undefined);
    };

    handleScroll();
    scrollArea.addEventListener("scroll", handleScroll, { passive: true });
    return () => scrollArea.removeEventListener("scroll", handleScroll);
  }, [scrollArea, userMessageCount]);

  useLayoutEffect(() => {
    if (!scrollArea) return;

    const currentHeight = scrollArea.scrollHeight;
    const hasNewContent = currentHeight > lastScrollHeightRef.current;
    const isFirstRender = lastScrollHeightRef.current === 0;
    const isNewUserMessage = messages.at(-1)?.role === "user";

    if (
      hasNewContent &&
      (isFirstRender || isAtBottomRef.current || isNewUserMessage)
    ) {
      scrollToBottom(isFirstRender || serverStatus === "running" ? "auto" : "smooth");
    } else if (hasNewContent) {
      setUnreadCount((count) => count + 1);
    }
    lastScrollHeightRef.current = currentHeight;
  }, [contentSignature, messages, scrollArea, scrollToBottom, serverStatus]);

  return (
    <div className="relative min-h-0 flex-1">
      <div
        className="h-full overflow-y-auto overscroll-contain"
        ref={setScrollArea}
      >
        {timeline.prelude.length === 0 && timeline.tasks.length === 0 ? (
          <EmptyState
            serverStatus={serverStatus}
            agentType={agentType}
            onSelectPrompt={onSelectPrompt}
          />
        ) : (
          <>
          <div className="sticky top-0 z-10 border-b bg-background/90 px-3 py-2 backdrop-blur-xl sm:px-6">
            <div className="mx-auto flex w-full max-w-5xl flex-wrap items-center gap-2">
              <label className="flex min-h-9 min-w-48 flex-1 items-center gap-2 rounded-lg border bg-background px-3 text-xs">
                <Search className="size-3.5 text-muted-foreground" />
                <span className="sr-only">Search all tasks</span>
                <input
                  ref={searchInputRef}
                  type="search"
                  value={taskQuery}
                  onChange={(event) => setTaskQuery(event.target.value)}
                  placeholder="Search tasks, output, and tools…"
                  className="min-w-0 flex-1 bg-transparent outline-none"
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && taskQuery) {
                      event.preventDefault();
                      navigateSearchResults(event.shiftKey ? -1 : 1);
                    }
                  }}
                />
                {taskQuery && (
                  <button
                    type="button"
                    onClick={() => setTaskQuery("")}
                    aria-label="Clear task search"
                  >
                    <X className="size-3.5" />
                  </button>
                )}
              </label>
              <label>
                <span className="sr-only">Filter tasks</span>
                <select
                  value={taskFilter}
                  onChange={(event) =>
                    setTaskFilter(event.target.value as TaskFilter)
                  }
                  className="min-h-9 rounded-lg border bg-background px-3 text-xs outline-none focus:ring-2 focus:ring-ring"
                >
                  <option value="all">All tasks</option>
                  <option value="running">Running</option>
                  <option value="queued">Queued</option>
                  <option value="failed">Failed</option>
                  <option value="completed">Completed</option>
                  <option value="tool-error">Tool errors</option>
                </select>
              </label>
              <span className="text-xs text-muted-foreground" role="status">
                {taskQuery
                  ? `${filteredTasks.length} tasks · ${totalMatchCount} matches`
                  : `${filteredTasks.length} of ${timeline.tasks.length}`}
              </span>
              {taskQuery && filteredTasks.length > 0 && (
                <div className="flex overflow-hidden rounded-md border bg-background">
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="size-8 rounded-none"
                    onClick={() => navigateSearchResults(-1)}
                    title="Previous matching task"
                  >
                    <ArrowLeft />
                    <span className="sr-only">Previous matching task</span>
                  </Button>
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="size-8 rounded-none border-l"
                    onClick={() => navigateSearchResults(1)}
                    title="Next matching task"
                  >
                    <ArrowRight />
                    <span className="sr-only">Next matching task</span>
                  </Button>
                </div>
              )}
            </div>
          </div>
          <div className="mx-auto flex w-full max-w-5xl flex-col gap-7 px-3 py-6 sm:px-6 sm:py-10">
            {timeline.prelude.map((message, index) => (
              <MessageItem key={`prelude-${message.id ?? index}`} message={message} />
            ))}
            {hiddenTaskCount > 0 && (
              <Button
                type="button"
                variant="outline"
                onClick={() => setShowAllTasks(true)}
                className="mx-auto rounded-full"
              >
                Show {hiddenTaskCount} older{" "}
                {hiddenTaskCount === 1 ? "task" : "tasks"}
              </Button>
            )}
            {visibleTasks.length === 0 && (
              <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
                No tasks match the current search and filter.
              </div>
            )}
            {visibleTasks.map(({task, index, status}) => {
              const searchResultIndex = filteredTasks.findIndex(
                (entry) => entry.task.key === task.key,
              );
              return (
              <TaskGroup
                key={task.key}
                task={task}
                number={index + 1}
                status={status}
                onRetryMessage={onRetryMessage}
                onEditMessage={onEditMessage}
                onDismissMessage={onDismissMessage}
                onRunTask={onRunTask}
                onEditTask={(content) => onSelectPrompt?.(content)}
                onFollowUp={(content) =>
                  onSelectPrompt?.(`Follow up on this task:\n\n${content}\n\n`)
                }
                onStopTask={onStopTask}
                searchQuery={taskQuery}
                searchResultIndex={searchResultIndex}
                isCurrentSearchResult={
                  Boolean(taskQuery) &&
                  searchResultIndex === currentSearchResult
                }
              />
              );
            })}
          </div>
          </>
        )}
      </div>

      {(canScrollToPreviousUser || canScrollToNextUser) && (
        <div className="absolute bottom-4 right-3 z-10 flex overflow-hidden rounded-full border bg-background/95 shadow-lg backdrop-blur sm:right-4">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            disabled={!canScrollToPreviousUser}
            onClick={scrollToPreviousUserMessage}
            className="size-11 rounded-none"
            title="Previous task"
          >
            <ArrowUp />
            <span className="sr-only">Jump to previous task</span>
          </Button>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            disabled={!canScrollToNextUser}
            onClick={scrollToNextUserMessage}
            className="size-11 rounded-none border-l"
            title="Next task"
          >
            <ArrowDown />
            <span className="sr-only">Jump to next task</span>
          </Button>
        </div>
      )}

      {showScrollButton && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => scrollToBottom()}
          className="absolute bottom-4 left-3 z-10 h-11 gap-2 rounded-full bg-background/95 px-3 shadow-lg backdrop-blur sm:left-1/2 sm:-translate-x-1/2"
          title="Jump to latest message"
        >
          <ArrowDown className="size-3.5" />
          <span>
            {unreadCount > 0
              ? `${unreadCount} new ${unreadCount === 1 ? "update" : "updates"}`
              : "Jump to latest"}
          </span>
        </Button>
      )}
    </div>
  );
}

function getPreviousUserMessageTop(scrollArea: HTMLDivElement) {
  const scrollAreaTop = scrollArea.getBoundingClientRect().top;
  const userMessages = Array.from(
    scrollArea.querySelectorAll<HTMLElement>("[data-user-message]"),
  );

  return userMessages
    .map(
      (message) =>
        message.getBoundingClientRect().top -
        scrollAreaTop +
        scrollArea.scrollTop,
    )
    .findLast((position) => position < scrollArea.scrollTop - 8);
}

function getNextUserMessageTop(scrollArea: HTMLDivElement) {
  const scrollAreaTop = scrollArea.getBoundingClientRect().top;
  const positions = Array.from(
    scrollArea.querySelectorAll<HTMLElement>("[data-user-message]"),
  ).map(
    (message) =>
      message.getBoundingClientRect().top -
      scrollAreaTop +
      scrollArea.scrollTop,
  );
  return positions.find((position) => position > scrollArea.scrollTop + 24);
}

function collectToolCalls(richMessages: RichMessage[]): ToolCall[] {
  const calls = new Map<string, ToolCall>();

  for (const message of richMessages) {
    for (const block of message.content) {
      if (block.type === "tool_use" && block.tool_use_id) {
        calls.set(block.tool_use_id, {
          ...calls.get(block.tool_use_id),
          id: block.tool_use_id,
          name: block.tool_name || "Tool",
          input: block.tool_input,
          status: block.status || "running",
          timestamp: message.timestamp,
        });
      }

      if (block.type === "tool_result" && block.tool_use_id) {
        const existing = calls.get(block.tool_use_id);
        calls.set(block.tool_use_id, {
          id: block.tool_use_id,
          name: existing?.name || "Tool",
          input: existing?.input,
          result: block.text ?? "",
          status: block.status || (block.is_error ? "failed" : "completed"),
          isError: block.status === "failed" || block.is_error,
          timestamp: existing?.timestamp || message.timestamp,
        });
      }
    }
  }

  return [...calls.values()];
}

function findTaskAtTime(tasks: TaskSection[], timestamp: string) {
  const targetTime = Date.parse(timestamp);
  if (Number.isNaN(targetTime)) return undefined;

  let low = 0;
  let high = tasks.length - 1;
  let match: TaskSection | undefined;
  while (low <= high) {
    const middle = Math.floor((low + high) / 2);
    const promptTime = tasks[middle].prompt.time
      ? Date.parse(tasks[middle].prompt.time!)
      : Number.NaN;
    if (Number.isNaN(promptTime) || promptTime > targetTime) {
      high = middle - 1;
    } else {
      match = tasks[middle];
      low = middle + 1;
    }
  }
  return match;
}

function formatToolInput(input: unknown): string {
  if (input === undefined || input === null) return "";
  if (typeof input === "string") {
    try {
      return JSON.stringify(JSON.parse(input), null, 2);
    } catch {
      return input;
    }
  }

  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

function ToolCallCard({
  toolCall,
  searchQuery = "",
}: {
  toolCall: ToolCall;
  searchQuery?: string;
}) {
  const isFailed = toolCall.status === "failed" || Boolean(toolCall.isError);
  const isPending =
    toolCall.status === "running" ||
    (toolCall.status === undefined && toolCall.result === undefined);
  const input = formatToolInput(toolCall.input);
  const [isOpen, setIsOpen] = useState(
    Boolean(searchQuery) || isPending || isFailed,
  );

  useEffect(() => {
    if (searchQuery || isPending || isFailed) setIsOpen(true);
  }, [isFailed, isPending, searchQuery]);

  return (
    <details
      className="group overflow-hidden rounded-lg border-l-2 border-y-0 border-r-0 bg-muted/20"
      open={isOpen}
      onToggle={(event) => setIsOpen(event.currentTarget.open)}
    >
      <summary className="flex cursor-pointer list-none items-center gap-3 px-4 py-3 transition hover:bg-muted/45 [&::-webkit-details-marker]:hidden">
        <span className="grid size-8 shrink-0 place-items-center rounded-lg border bg-background">
          <Wrench className="size-4" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">
            {toolCall.name}
          </span>
          <span className="block text-xs text-muted-foreground">
            {isFailed
              ? "Tool call failed"
              : isPending
                ? "Tool call is running"
                : "Tool call completed"}
          </span>
        </span>
        {isFailed ? (
          <CircleAlert className="size-4 shrink-0 text-destructive" />
        ) : isPending ? (
          <LoaderCircle className="size-4 shrink-0 animate-spin text-muted-foreground" />
        ) : (
          <CheckCircle2 className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
        )}
        <ArrowDown className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
      </summary>
      <div className="space-y-4 border-t bg-muted/20 px-4 py-4">
        {input && (
          <ToolDetail label="Input" content={input} searchQuery={searchQuery} />
        )}
        {toolCall.result !== undefined && (
          <ToolDetail
            label={isFailed ? "Error" : "Result"}
            content={toolCall.result || "(No output)"}
            searchQuery={searchQuery}
          />
        )}
        {!input && toolCall.result === undefined && (
          <p className="text-xs text-muted-foreground">
            No tool details are available yet.
          </p>
        )}
      </div>
    </details>
  );
}

function ToolCallGroup({
  toolCalls,
  searchQuery,
}: {
  toolCalls: ToolCall[];
  searchQuery: string;
}) {
  const pending = toolCalls.filter((tool) => tool.result === undefined).length;
  const failed = toolCalls.filter((tool) => tool.isError).length;
  const [isOpen, setIsOpen] = useState(
    Boolean(searchQuery) || pending > 0 || failed > 0,
  );

  useEffect(() => {
    if (searchQuery || pending > 0 || failed > 0) setIsOpen(true);
  }, [failed, pending, searchQuery]);

  return (
    <details
      className="group ml-3 overflow-hidden rounded-xl border bg-muted/15 sm:ml-8"
      open={isOpen}
      onToggle={(event) => setIsOpen(event.currentTarget.open)}
    >
      <summary className="flex min-h-11 cursor-pointer list-none items-center gap-3 px-4 py-2.5 [&::-webkit-details-marker]:hidden">
        <Wrench className="size-4 text-muted-foreground" />
        <span className="min-w-0 flex-1 text-sm font-medium">
          Tool activity · {toolCalls.length}
        </span>
        <span className="text-xs text-muted-foreground">
          {failed > 0
            ? `${failed} failed`
            : pending > 0
              ? `${pending} running`
              : "Completed"}
        </span>
        <ArrowDown className="size-4 text-muted-foreground transition-transform group-open:rotate-180" />
      </summary>
      <div className="space-y-1 border-t p-2">
        {toolCalls.map((toolCall) => (
          <ToolCallCard
            key={toolCall.id}
            toolCall={toolCall}
            searchQuery={searchQuery}
          />
        ))}
      </div>
    </details>
  );
}

function ToolDetail({
  label,
  content,
  searchQuery,
}: {
  label: string;
  content: string;
  searchQuery: string;
}) {
  return (
    <section>
      <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </h3>
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border bg-background p-3 font-mono text-xs leading-5">
        <HighlightedText content={content} query={searchQuery} />
      </pre>
    </section>
  );
}

function TaskGroup({
  task,
  number,
  status,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  onRunTask,
  onEditTask,
  onFollowUp,
  onStopTask,
  searchQuery,
  searchResultIndex,
  isCurrentSearchResult,
}: {
  task: TaskSection;
  number: number;
  status: TaskStatus;
  onRetryMessage: (clientId: string) => Promise<boolean>;
  onEditMessage: (clientId: string, content: string) => void;
  onDismissMessage: (clientId: string) => void;
  onRunTask: (content: string) => void;
  onEditTask: (content: string) => void;
  onFollowUp: (content: string) => void;
  onStopTask: () => void;
  searchQuery: string;
  searchResultIndex: number;
  isCurrentSearchResult: boolean;
}) {
  const statusMeta = {
    queued: {
      label: "Queued",
      icon: Clock3,
      className: "text-muted-foreground",
    },
    running: {
      label: "Running",
      icon: LoaderCircle,
      className: "text-amber-600 dark:text-amber-400",
    },
    completed: {
      label: "Completed",
      icon: CheckCircle2,
      className: "text-emerald-600 dark:text-emerald-400",
    },
    failed: {
      label: "Failed",
      icon: CircleAlert,
      className: "text-destructive",
    },
  }[status];
  const StatusIcon = statusMeta.icon;
  const activity = getTaskActivity(task);
  const groupedActivity = groupConsecutiveTools(activity);
  const markdown = taskToMarkdown(toSearchableTask(task), number);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const latestTool = [...task.toolCalls].reverse().find(
    (tool) => tool.result === undefined,
  ) ?? task.toolCalls.at(-1);

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

  const copyTask = async () => {
    try {
      await navigator.clipboard.writeText(markdown);
      toast.success("Task copied");
    } catch {
      toast.error("Could not copy the task");
    }
  };

  const exportTask = () => {
    const url = URL.createObjectURL(
      new Blob([markdown], {type: "text/markdown;charset=utf-8"}),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = `agentapi-task-${number}.md`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  return (
    <section
      className={`overflow-hidden rounded-2xl border bg-background/70 transition ${
        status === "running"
          ? "border-amber-500/50 shadow-md ring-1 ring-amber-500/15"
          : status === "completed"
            ? "shadow-none"
            : "shadow-sm"
      } ${isCurrentSearchResult ? "ring-2 ring-primary/50" : ""}`}
      data-status={status}
      data-search-result={searchResultIndex}
    >
      <header
        className={`flex min-h-11 items-center justify-between gap-3 border-b px-4 py-2 ${
          status === "running" ? "bg-amber-500/10" : "bg-muted/20"
        }`}
      >
        <div className="min-w-0">
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Task {number}
          </span>
          {status === "running" && (
            <p className="truncate text-xs text-foreground">
              {latestTool ? `Using ${latestTool.name}` : "Processing task"}
              {" · "}
              {formatElapsedTime(elapsedSeconds)}
            </p>
          )}
        </div>
        <div className="flex items-center gap-1">
          <span
            className={`flex items-center gap-1.5 text-xs font-medium ${statusMeta.className}`}
            role="status"
          >
            <StatusIcon
              className={`size-3.5 ${status === "running" ? "motion-safe:animate-spin" : ""}`}
            />
            {statusMeta.label}
          </span>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                size="icon"
                variant="ghost"
                className="size-8"
                title={`Task ${number} actions`}
              >
                <MoreHorizontal />
                <span className="sr-only">Task {number} actions</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                onSelect={() => onRunTask(task.prompt.content)}
              >
                <RefreshCw />
                Run again
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() => onEditTask(task.prompt.content)}
              >
                <Pencil />
                Edit and resend
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() => onFollowUp(task.prompt.content)}
              >
                <MessageSquarePlus />
                Create follow-up
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
          {status === "running" && (
            <Button
              type="button"
              size="sm"
              variant="destructive"
              className="h-8"
              onClick={onStopTask}
            >
              Stop
            </Button>
          )}
        </div>
      </header>
      <div className={`p-4 sm:p-5 ${status === "completed" ? "space-y-4" : "space-y-6"}`}>
        <MessageItem
          message={task.prompt}
          onRetryMessage={onRetryMessage}
          onEditMessage={onEditMessage}
          onDismissMessage={onDismissMessage}
          searchQuery={searchQuery}
        />
        {groupedActivity.map((item) =>
          item.type === "message" ? (
            <MessageItem
              key={item.key}
              message={item.message}
              searchQuery={searchQuery}
            />
          ) : item.type === "tool-group" ? (
            <ToolCallGroup
              key={item.key}
              toolCalls={item.toolCalls}
              searchQuery={searchQuery}
            />
          ) : (
            <div key={item.key} className="ml-3 sm:ml-8">
              <ToolCallCard
                toolCall={item.toolCall}
                searchQuery={searchQuery}
              />
            </div>
          ),
        )}
      </div>
    </section>
  );
}

function getTaskActivity(task: TaskSection): TaskActivity[] {
  if (task.richActivity.some((item) => item.type === "message")) {
    return task.richActivity;
  }

  return [
    ...task.responses.map((message, index) => ({
      type: "message" as const,
      key: `response-${message.id ?? index}`,
      message,
    })),
    ...task.toolCalls.map((toolCall) => ({
      type: "tool" as const,
      key: `tool-${toolCall.id}`,
      toolCall,
    })),
  ].sort((left, right) => {
    const leftTime =
      left.type === "message" ? left.message.time : left.toolCall.timestamp;
    const rightTime =
      right.type === "message" ? right.message.time : right.toolCall.timestamp;
    if (!leftTime) return 1;
    if (!rightTime) return -1;
    return Date.parse(leftTime) - Date.parse(rightTime);
  });
}

function toSearchableTask(task: TaskSection) {
  const activity = getTaskActivity(task);
  return {
    prompt: task.prompt.content,
    responses: activity
      .filter(
        (item): item is Extract<TaskActivity, {type: "message"}> =>
          item.type === "message",
      )
      .map((item) => item.message.content),
    tools: task.toolCalls.map((tool) => ({
      name: tool.name,
      input: formatToolInput(tool.input),
      result: tool.result,
      isError: tool.isError,
    })),
  };
}

function countTaskMatches(task: TaskSection, query: string) {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return 0;
  const searchable = toSearchableTask(task);
  return [
    searchable.prompt,
    ...searchable.responses,
    ...searchable.tools.flatMap((tool) => [
      tool.name,
      tool.input ?? "",
      tool.result ?? "",
    ]),
  ].reduce(
    (total, content) =>
      total + content.toLocaleLowerCase().split(normalized).length - 1,
    0,
  );
}

function HighlightedText({
  content,
  query,
}: {
  content: string;
  query: string;
}) {
  const normalized = query.trim();
  if (!normalized) return content;
  const expression = new RegExp(
    `(${normalized.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")})`,
    "giu",
  );
  return content.split(expression).map((part, index) =>
    part.toLocaleLowerCase() === normalized.toLocaleLowerCase() ? (
      <mark
        key={`${index}-${part}`}
        className="rounded-sm bg-amber-300 px-0.5 text-black"
      >
        {part}
      </mark>
    ) : (
      <React.Fragment key={`${index}-${part}`}>{part}</React.Fragment>
    ),
  );
}

function formatElapsedTime(seconds: number) {
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}

function EmptyState({
  serverStatus,
  agentType,
  onSelectPrompt,
}: {
  serverStatus: ServerStatus;
  agentType: AgentType;
  onSelectPrompt?: (prompt: string) => void;
}) {
  const isOffline = serverStatus === "offline";
  const name =
    agentType === "unknown" ? "your coding agent" : agentType.replace("-", " ");

  return (
    <div className="mx-auto flex h-full w-full max-w-3xl flex-col items-center justify-center px-6 py-12 text-center">
      <div className="relative mb-7">
        <div className="absolute inset-0 scale-150 rounded-full bg-primary/10 blur-2xl" />
        <div className="relative grid size-16 place-items-center rounded-2xl border bg-card shadow-sm">
          <TerminalSquare className="size-7" />
        </div>
      </div>
      <p className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">
        <Sparkles className="size-3.5" />
        Live agent workspace
      </p>
      <h1 className="max-w-xl text-balance text-2xl font-semibold tracking-tight sm:text-3xl">
        {isOffline ? "The agent server is offline" : `Start working with ${name}`}
      </h1>
      <p className="mt-3 max-w-lg text-pretty text-sm leading-6 text-muted-foreground">
        {isOffline
          ? "AgentAPI is trying to reconnect. Check the server URL and make sure the agent process is running."
            : "Send a task, attach project files, or switch to Terminal input when the agent needs direct keystrokes."}
      </p>
      {!isOffline && (
        <div className="mt-8 grid w-full max-w-lg grid-cols-1 gap-3 text-left sm:grid-cols-2">
          <PromptHint
            icon={Code2}
            text="Review the current codebase"
            prompt="Review the current codebase and suggest the highest-impact improvements."
            onSelect={onSelectPrompt}
          />
          <PromptHint
            icon={TerminalSquare}
            text="Investigate a failing test"
            prompt="Run the test suite, investigate any failures, and explain the root cause."
            onSelect={onSelectPrompt}
          />
        </div>
      )}
    </div>
  );
}

function PromptHint({
  icon: Icon,
  text,
  prompt,
  onSelect,
}: {
  icon: React.ComponentType<{ className?: string }>;
  text: string;
  prompt: string;
  onSelect?: (prompt: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onSelect?.(prompt)}
      className="flex min-h-12 items-center gap-3 rounded-xl border bg-card/60 p-3 text-left text-xs text-muted-foreground shadow-xs transition hover:bg-card hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2"
    >
      <Icon className="size-4 shrink-0 text-foreground" />
      <span>{text}</span>
    </button>
  );
}

function MessageItem({
  message,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  searchQuery: globalSearchQuery = "",
}: {
  message: Message | DraftMessage;
  onRetryMessage?: (clientId: string) => Promise<boolean>;
  onEditMessage?: (clientId: string, content: string) => void;
  onDismissMessage?: (clientId: string) => void;
  searchQuery?: string;
}) {
  const isUser = message.role === "user";
  const isDraft = message.id === undefined;
  const draft = isDraft ? (message as DraftMessage) : undefined;
  const isFailed = draft?.deliveryStatus === "failed";
  const [outputMode, setOutputMode] = useState<"raw" | "rendered">("raw");
  const [searchOpen, setSearchOpen] = useState(false);
  const [outputSearchQuery, setOutputSearchQuery] = useState("");
  const effectiveSearchQuery = outputSearchQuery || globalSearchQuery;
  const matchCount =
    outputSearchQuery.trim() === ""
      ? 0
      : message.content
          .toLocaleLowerCase()
          .split(outputSearchQuery.trim().toLocaleLowerCase()).length - 1;

  if (!isUser) {
    return (
      <article className="min-w-0">
        <div className="mb-2 flex min-h-9 flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
            <TerminalSquare className="size-3.5" />
            <span>Agent output</span>
            {message.time && (
              <time
                dateTime={message.time}
                className="font-normal normal-case tracking-normal"
                title={new Date(message.time).toLocaleString()}
              >
                {formatMessageTime(message.time)}
              </time>
            )}
            {isDraft && (
              <span className="normal-case tracking-normal">Updating…</span>
            )}
          </div>
          {message.content && (
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() =>
                  setOutputMode((mode) =>
                    mode === "raw" ? "rendered" : "raw",
                  )
                }
                className="grid size-9 place-items-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
                title={
                  outputMode === "raw"
                    ? "Render Markdown"
                    : "Show raw terminal output"
                }
                aria-label={
                  outputMode === "raw"
                    ? "Render Markdown"
                    : "Show raw terminal output"
                }
              >
                {outputMode === "raw" ? (
                  <Eye className="size-3.5" />
                ) : (
                  <Code2 className="size-3.5" />
                )}
              </button>
              <button
                type="button"
                onClick={() => {
                  setSearchOpen((open) => !open);
                  if (searchOpen) setOutputSearchQuery("");
                }}
                className="grid size-9 place-items-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
                title="Search output"
                aria-label="Search output"
                aria-expanded={searchOpen}
              >
                <Search className="size-3.5" />
              </button>
              <CopyButton content={message.content} />
            </div>
          )}
        </div>
        {searchOpen && (
          <label className="mb-2 flex items-center gap-2 rounded-lg border bg-muted/20 px-3 py-2 text-xs">
            <Search className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="sr-only">Search this agent output</span>
            <input
              autoFocus
              type="search"
              value={outputSearchQuery}
              onChange={(event) => setOutputSearchQuery(event.target.value)}
              placeholder="Search this output…"
              className="min-w-0 flex-1 bg-transparent outline-none"
            />
            {outputSearchQuery && (
              <span className="shrink-0 text-muted-foreground" role="status">
                {matchCount} {matchCount === 1 ? "match" : "matches"}
              </span>
            )}
          </label>
        )}
        {message.content === "" ? (
          <LoadingDots />
        ) : (
          <div className="h-[7.5rem] overflow-y-auto overscroll-contain">
            <ProcessedMessage
              messageContent={message.content}
              isUser={false}
              renderMode={outputMode === "rendered" ? "markdown" : "raw"}
              searchQuery={effectiveSearchQuery}
            />
          </div>
        )}
      </article>
    );
  }

  return (
    <article
      className="flex scroll-mt-4 flex-row-reverse gap-3 border-t pt-7 first:border-t-0 first:pt-0 sm:gap-4"
      data-user-message
    >
      <div
        className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg border bg-foreground text-background shadow-xs"
      >
        <User className="size-4" />
      </div>
      <div className="min-w-0 max-w-[85%]">
        <div className="mb-1.5 flex min-h-9 items-center justify-end gap-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
          <div className="flex items-center gap-2">
            <span>You</span>
            {message.time && (
              <time
                dateTime={message.time}
                className="font-normal normal-case tracking-normal"
                title={new Date(message.time).toLocaleString()}
              >
                {formatMessageTime(message.time)}
              </time>
            )}
          {isDraft && !isFailed && (
            <span className="normal-case tracking-normal">Sending…</span>
          )}
          {isFailed && (
            <span className="normal-case tracking-normal text-destructive">
              Not sent
            </span>
          )}
          </div>
          {message.content && (
            <CopyButton content={message.content} label="task" />
          )}
        </div>
        <div className="rounded-2xl rounded-tr-md bg-foreground px-4 py-3 text-sm leading-6 text-background shadow-sm">
          {message.content === "" ? (
            <LoadingDots />
          ) : (
            <ProcessedMessage
              messageContent={message.content}
              isUser={isUser}
              searchQuery={globalSearchQuery}
            />
          )}
        </div>
        {isFailed && draft && (
          <div
            className="mt-2 flex flex-wrap justify-end gap-1"
            role="alert"
            aria-label={`Message was not sent${draft.error ? `: ${draft.error}` : ""}`}
          >
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void onRetryMessage?.(draft.clientId)}
            >
              <RefreshCw />
              Retry
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => onEditMessage?.(draft.clientId, draft.content)}
            >
              <Pencil />
              Edit
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={() => onDismissMessage?.(draft.clientId)}
              title="Dismiss failed message"
            >
              <X />
              <span className="sr-only">Dismiss failed message</span>
            </Button>
          </div>
        )}
      </div>
    </article>
  );
}

function CopyButton({
  content,
  label = "response",
}: {
  content: string;
  label?: "task" | "response";
}) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard access can be blocked in embedded or non-secure contexts.
      setCopied(false);
      toast.error(`Could not copy the ${label}`, {
        description: "Clipboard access may be blocked in this browser context.",
      });
    }
  };

  return (
    <button
      type="button"
      onClick={copy}
      className="grid size-11 shrink-0 place-items-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground sm:size-9"
      title={copied ? `Copied ${label}` : `Copy ${label}`}
      aria-label={copied ? `Copied ${label}` : `Copy ${label}`}
    >
      {copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}
    </button>
  );
}

const LoadingDots = () => (
  <div
    className="flex h-6 items-center gap-1.5"
    role="status"
    aria-live="polite"
    aria-label="Agent is responding"
  >
    {[0, 150, 300].map((delay) => (
      <span
        key={delay}
        className="size-1.5 animate-pulse rounded-full bg-muted-foreground"
        style={{ animationDelay: `${delay}ms` }}
      />
    ))}
  </div>
);

function formatMessageTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
}
