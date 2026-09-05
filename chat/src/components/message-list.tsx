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
  Code2,
  Download,
  FileText,
  LoaderCircle,
  Search,
  SlidersHorizontal,
  X,
} from "lucide-react";
import { Button } from "./ui/button";
import {useChat, AgentType} from "./chat-provider";
import type {
  DraftMessage,
  Message,
  RichMessage,
  ServerStatus,
} from "./chat-provider";
import {taskMatchesQuery} from "@/lib/task-actions";
import {uiCopy} from "@/lib/ui-copy";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import {contentFingerprint} from "@/lib/content-fingerprint";
import {formatDateLabel} from "@/lib/format-time";
import {getPreviousUserMessageTop, getNextUserMessageTop} from "@/lib/scroll-anchors";
import {
  type TaskSection,
  type TaskFilter,
  type ToolCall,
  collectToolCalls,
  findTaskAtTime,
  getTaskStatus,
  toSearchableTask,
  countTaskMatches,
} from "@/lib/task-timeline";
import {MessageItem} from "./message-list/message-item";
import {TaskGroup} from "./message-list/task-group";
import {EmptyState} from "./message-list/empty-state";

const agentRenderModeStorageKey = "agentapi.chat.agent-output-render-mode";

// How many recent tasks to show before collapsing older ones behind a
// "Show N older tasks" button. Keeps the initial render lightweight for
// long sessions. Full virtualization (e.g. react-virtuoso) would be the
// proper fix for very long conversations.
const VISIBLE_TASKS_DEFAULT = 5;

interface MessageListProps {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  serverStatus: ServerStatus;
  agentType: AgentType;
  onSelectPrompt?: (prompt: string) => void;
  onRetryMessage: (clientId: string) => Promise<boolean>;
  onEditMessage: (clientId: string, content: string) => void;
  onDismissMessage: (clientId: string) => void;
  onStopTask: () => void;
  headerAction?: React.ReactNode;
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
  onStopTask,
  headerAction,
}: MessageListProps) {
  const {downloadSession} = useChat();
  const [scrollArea, setScrollArea] = useState<HTMLDivElement | null>(null);
  const [showScrollButton, setShowScrollButton] = useState(false);
  const [unreadCount, setUnreadCount] = useState(0);
  const [canScrollToPreviousUser, setCanScrollToPreviousUser] =
    useState(false);
  const [canScrollToNextUser, setCanScrollToNextUser] = useState(false);
  const [showAllTasks, setShowAllTasks] = useState(false);
  const [taskQuery, setTaskQuery] = useState("");
  const [taskFilter, setTaskFilter] = useState<TaskFilter>("all");
  const [globalRenderMode, setGlobalRenderMode] = useState<"raw" | "markdown">(() => {
    if (typeof window === "undefined") return "raw";
    return window.localStorage.getItem(agentRenderModeStorageKey) === "markdown" ? "markdown" : "raw";
  });
  const toggleGlobalRenderMode = () => {
    setGlobalRenderMode((current) => {
      const next = current === "markdown" ? "raw" : "markdown";
      try { window.localStorage.setItem(agentRenderModeStorageKey, next); } catch { /* best-effort */ }
      return next;
    });
  };
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

    const callsByID = new Map<string, ToolCall>(toolCalls.map((tc) => [tc.id, tc]));
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
          const tc = callsByID.get(block.tool_use_id);
          if (tc) {
            target.richActivity.push({
              type: "tool",
              key: `rich-tool-${block.tool_use_id}`,
              toolCall: tc,
            });
          }
        }
      });
    }

    return {prelude, tasks};
  }, [messages, richMessages, toolCalls]);
  const [downloadingConversation, setDownloadingConversation] = useState(false);
  const exportConversation = () => {
    setDownloadingConversation(true);
    void downloadSession()
      .catch(() => {})
      .finally(() => setDownloadingConversation(false));
  };
  const filteredTasks = useMemo(
    () =>
      timeline.tasks
        .map((task, index) => ({
          task,
          index,
          status: getTaskStatus(task, index, timeline.tasks.length, serverStatus),
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
    (total, task) => total + task.matchCount, 0,
  );
  const hiddenTaskCount =
    !filtersActive && !showAllTasks && filteredTasks.length > VISIBLE_TASKS_DEFAULT
      ? filteredTasks.length - VISIBLE_TASKS_DEFAULT
      : 0;
  const visibleTasks =
    hiddenTaskCount > 0 ? filteredTasks.slice(hiddenTaskCount) : filteredTasks;
  const contentSignature = useMemo(
    () =>
      [
        ...timeline.prelude.map((message, index) =>
          `prelude-${message.id ?? index}:${contentFingerprint(message.content)}`),
        ...timeline.tasks.map((task) =>
          `${task.key}:${contentFingerprint(task.prompt.content)}:${task.responses
            .map((message) => contentFingerprint(message.content))
            .join(",")}:${task.toolCalls
            .map((tool) => `${tool.id}:${contentFingerprint(tool.result ?? "")}`)
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
    scrollArea.scrollTo({ top: Math.max(0, targetTop - 16), behavior: "smooth" });
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
      setCanScrollToPreviousUser(getPreviousUserMessageTop(scrollArea) !== undefined);
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
          <div className="sticky top-0 z-10 border-b bg-background/90 px-3 py-1 backdrop-blur-xl sm:px-6 sm:py-2">
            <div className="mx-auto flex w-full max-w-5xl flex-wrap items-center gap-1.5 sm:gap-2">
              <label className="flex min-h-8 min-w-48 flex-1 items-center gap-2 rounded-lg border bg-background px-3 text-xs sm:min-h-9">
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
                    className="grid size-7 place-items-center rounded-md outline-none transition hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <X className="size-3.5" />
                  </button>
                )}
              </label>
              <label className="hidden sm:block">
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
              <span className="hidden text-xs text-muted-foreground sm:inline" role="status">
                {taskQuery
                  ? `${filteredTasks.length} tasks · ${totalMatchCount} matches`
                  : `${filteredTasks.length} of ${timeline.tasks.length}`}
              </span>
              <button
                type="button"
                onClick={toggleGlobalRenderMode}
                className={`grid size-9 place-items-center rounded-md outline-none transition hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring ${
                  globalRenderMode === "markdown" ? "bg-muted text-foreground" : "text-muted-foreground"
                }`}
                title={globalRenderMode === "markdown" ? "Show raw terminal output" : "Preview as Markdown"}
                aria-label={globalRenderMode === "markdown" ? "Show raw terminal output" : "Preview as Markdown"}
                aria-pressed={globalRenderMode === "markdown"}
              >
                {globalRenderMode === "markdown" ? <Code2 className="size-3.5" /> : <FileText className="size-3.5" />}
              </button>
              <Button
                type="button"
                size="sm"
                variant="outline"
                className="hidden h-9 shrink-0 sm:inline-flex"
                onClick={exportConversation}
                disabled={downloadingConversation}
                title="Download the session timeline as JSONL"
              >
                {downloadingConversation ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Download />
                )}
                <span className="hidden sm:inline">Conversation JSONL</span>
                <span className="sr-only sm:hidden">Download conversation JSONL</span>
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    type="button"
                    size="icon"
                    variant="outline"
                    className="size-9 shrink-0 sm:hidden"
                    title={uiCopy.taskToolbar.title}
                  >
                    <SlidersHorizontal />
                    <span className="sr-only">
                      Open {uiCopy.taskToolbar.title.toLowerCase()}
                    </span>
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-52">
                  <DropdownMenuLabel>
                    {taskQuery
                      ? `${filteredTasks.length} tasks · ${totalMatchCount} matches`
                      : `${filteredTasks.length} of ${timeline.tasks.length} tasks`}
                  </DropdownMenuLabel>
                  <DropdownMenuSeparator />
                  {(
                    [
                      ["all", uiCopy.taskToolbar.all],
                      ["running", uiCopy.taskToolbar.running],
                      ["queued", uiCopy.taskToolbar.queued],
                      ["failed", uiCopy.taskToolbar.failed],
                      ["completed", uiCopy.taskToolbar.completed],
                      ["tool-error", uiCopy.taskToolbar.toolErrors],
                    ] as const
                  ).map(([value, label]) => (
                    <DropdownMenuItem
                      key={value}
                      onSelect={() => setTaskFilter(value)}
                    >
                      {label}
                    </DropdownMenuItem>
                  ))}
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    onSelect={exportConversation}
                    disabled={downloadingConversation}
                  >
                    <Download />
                    {uiCopy.taskToolbar.download}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
              {headerAction}
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
              <MessageItem key={`prelude-${message.id ?? index}`} message={message} renderMode={globalRenderMode} />
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
            {visibleTasks.map(({task, index, status}, visibleIndex) => {
              const searchResultIndex = filteredTasks.findIndex(
                (entry) => entry.task.key === task.key,
              );
              const prevDate = visibleIndex > 0
                ? visibleTasks[visibleIndex - 1].task.prompt.time
                : undefined;
              const currentDate = task.prompt.time;
              const showDateSep = currentDate && (
                !prevDate ||
                new Date(currentDate).toDateString() !== new Date(prevDate).toDateString()
              );
              return (
              <React.Fragment key={task.key}>
              {showDateSep && (
                <div className="flex items-center gap-3 text-xs text-muted-foreground">
                  <div className="h-px flex-1 bg-border" />
                  <span>{formatDateLabel(currentDate)}</span>
                  <div className="h-px flex-1 bg-border" />
                </div>
              )}
              <TaskGroup
                task={task}
                number={index + 1}
                status={status}
                onRetryMessage={onRetryMessage}
                onEditMessage={onEditMessage}
                onDismissMessage={onDismissMessage}
                onStopTask={onStopTask}
                searchQuery={taskQuery}
                searchResultIndex={searchResultIndex}
                isCurrentSearchResult={
                  Boolean(taskQuery) &&
                  searchResultIndex === currentSearchResult
                }
                renderMode={globalRenderMode}
              />
              </React.Fragment>
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
