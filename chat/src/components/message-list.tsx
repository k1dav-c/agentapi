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
  Search,
  X,
} from "lucide-react";
import { Button } from "./ui/button";
import {AgentType} from "./chat-provider";
import type {
  DraftMessage,
  Message,
  RichMessage,
  ServerStatus,
} from "./chat-provider";
import {taskMatchesQuery} from "@/lib/task-actions";
import {uiCopy} from "@/lib/ui-copy";
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
import {createPortal} from "react-dom";
import {HEADER_ACTIONS_ID} from "@/app/header";
import {useChat} from "./chat-provider";
import {TaskGroup} from "./message-list/task-group";
import {EmptyState, StartupScreen} from "./message-list/empty-state";

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
  onRetryMessage: (clientId: string) => Promise<boolean>;
  onEditMessage: (clientId: string, content: string) => void;
  onDismissMessage: (clientId: string) => void;
  onStopTask: () => void;
  onSendRaw?: (data: string) => void;
  headerAction?: React.ReactNode;
}

export default function MessageList({
  messages,
  richMessages,
  serverStatus,
  agentType,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  onStopTask,
  onSendRaw,
  headerAction,
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
  const [searchOpen, setSearchOpen] = useState(false);
  const {terminalPrompt} = useChat();
  // The page's actions render into the header's slot (one top bar); the
  // embed page has no header, so they get their own row there.
  const [headerSlot, setHeaderSlot] = useState<HTMLElement | null>(null);
  useEffect(() => setHeaderSlot(document.getElementById(HEADER_ACTIONS_ID)), []);
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
        if (block.type === "thinking" && block.thinking) {
          target.richActivity.push({
            type: "thinking",
            key: `rich-thinking-${message.message_id}-${index}`,
            content: block.thinking,
            timestamp: message.timestamp,
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

  // A reload lands at the latest task, so only that one renders right away.
  // Older tasks show their prompt and fill in one at a time while the browser
  // is idle, newest first: rendering every step of several long tasks at once
  // froze slower devices for seconds. Search and filters render everything.
  const [renderedTaskKeys, setRenderedTaskKeys] = useState<ReadonlySet<string>>(() => new Set());
  const olderTaskKeys = visibleTasks.slice(0, -1).map(({task}) => task.key).join("\n");
  useEffect(() => {
    if (filtersActive || olderTaskKeys === "") return;
    const next = olderTaskKeys.split("\n").reverse().find((key) => !renderedTaskKeys.has(key));
    if (next === undefined) return;
    const render = () => setRenderedTaskKeys((previous) => new Set(previous).add(next));
    if (typeof window.requestIdleCallback === "function") {
      const handle = window.requestIdleCallback(render, {timeout: 500});
      return () => window.cancelIdleCallback(handle);
    }
    const handle = window.setTimeout(render, 50);
    return () => window.clearTimeout(handle);
  }, [filtersActive, olderTaskKeys, renderedTaskKeys]);
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
        setSearchOpen(true);
        window.requestAnimationFrame(() => {
          searchInputRef.current?.focus();
          searchInputRef.current?.select();
        });
      } else if (
        event.key === "Escape" &&
        document.activeElement === searchInputRef.current
      ) {
        setTaskQuery("");
        setSearchOpen(false);
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
    let lastScrollTop = scrollArea.scrollTop;
    const handleScroll = () => {
      const { scrollTop, scrollHeight, clientHeight } = scrollArea;
      const atBottom = scrollTop + clientHeight >= scrollHeight - 32;
      // Only scrolling up leaves the bottom. The view can also end up above
      // the bottom when the area shrinks (the dock or composer grows); that
      // must not unpin it.
      if (atBottom) isAtBottomRef.current = true;
      else if (scrollTop < lastScrollTop - 2) isAtBottomRef.current = false;
      lastScrollTop = scrollTop;
      setShowScrollButton(!isAtBottomRef.current);
      if (isAtBottomRef.current) setUnreadCount(0);
      setCanScrollToPreviousUser(getPreviousUserMessageTop(scrollArea) !== undefined);
      setCanScrollToNextUser(getNextUserMessageTop(scrollArea) !== undefined);
    };
    handleScroll();
    scrollArea.addEventListener("scroll", handleScroll, { passive: true });
    return () => scrollArea.removeEventListener("scroll", handleScroll);
  }, [scrollArea, userMessageCount]);

  const isEmpty = timeline.tasks.length === 0;
  // Stay pinned to the bottom while content grows anywhere (older tasks
  // rendering progressively, a thinking note expanding) if the reader is
  // already there.
  useEffect(() => {
    if (!scrollArea) return;
    const content = scrollArea.firstElementChild;
    if (!content) return;
    const observer = new ResizeObserver(() => {
      if (isAtBottomRef.current) scrollArea.scrollTop = scrollArea.scrollHeight;
    });
    observer.observe(scrollArea);
    for (const child of Array.from(scrollArea.children)) observer.observe(child);
    return () => observer.disconnect();
  }, [scrollArea, isEmpty, searchOpen, filtersActive]);

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

  const actions = (
    <>
      <Button
        type="button"
        size="icon"
        variant="ghost"
        className="size-8 rounded-full text-muted-foreground"
        title="Search tasks (⌘F)"
        aria-label="Search tasks"
        aria-expanded={searchOpen}
        onClick={() => setSearchOpen((open) => !open)}
      >
        <Search className="size-4" />
      </Button>
      {headerAction}
    </>
  );

  return (
    <div className="relative min-h-0 flex-1">
      {headerSlot ? (
        createPortal(actions, headerSlot)
      ) : (
        <div className="flex items-center justify-end gap-0.5 border-b px-2 py-1">{actions}</div>
      )}
      <div
        className="h-full overflow-y-auto overscroll-contain"
        ref={setScrollArea}
      >
        {(searchOpen || filtersActive) && (
          <div className="sticky top-0 z-10 border-b bg-background/95 px-3 py-1.5 backdrop-blur-xl sm:px-6">
            <div className="mx-auto flex w-full max-w-[72rem] flex-wrap items-center gap-1.5 sm:gap-2">
              <label className="flex min-h-8 min-w-48 flex-1 items-center gap-2 rounded-md border bg-card px-2.5 text-xs">
                <Search className="size-3.5 text-muted-foreground" />
                <span className="sr-only">Search all tasks</span>
                <input
                  ref={searchInputRef}
                  autoFocus
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
              </label>
              <label>
                <span className="sr-only">Filter tasks</span>
                <select
                  value={taskFilter}
                  onChange={(event) => setTaskFilter(event.target.value as TaskFilter)}
                  className="min-h-8 rounded-md border bg-card px-2 text-xs outline-none focus:ring-2 focus:ring-ring"
                >
                  <option value="all">{uiCopy.taskToolbar.all}</option>
                  <option value="running">{uiCopy.taskToolbar.running}</option>
                  <option value="queued">{uiCopy.taskToolbar.queued}</option>
                  <option value="failed">{uiCopy.taskToolbar.failed}</option>
                  <option value="completed">{uiCopy.taskToolbar.completed}</option>
                  <option value="tool-error">{uiCopy.taskToolbar.toolErrors}</option>
                </select>
              </label>
              <span className="text-xs tabular-nums text-muted-foreground" role="status">
                {taskQuery
                  ? `${filteredTasks.length} tasks · ${totalMatchCount} matches`
                  : `${filteredTasks.length} of ${timeline.tasks.length}`}
              </span>
              {taskQuery && filteredTasks.length > 0 && (
                <div className="flex overflow-hidden rounded-md border bg-card">
                  <Button type="button" size="icon" variant="ghost" className="size-8 rounded-none" onClick={() => navigateSearchResults(-1)} title="Previous matching task">
                    <ArrowLeft />
                    <span className="sr-only">Previous matching task</span>
                  </Button>
                  <Button type="button" size="icon" variant="ghost" className="size-8 rounded-none border-l" onClick={() => navigateSearchResults(1)} title="Next matching task">
                    <ArrowRight />
                    <span className="sr-only">Next matching task</span>
                  </Button>
                </div>
              )}
              <Button
                type="button"
                size="icon"
                variant="ghost"
                className="size-8"
                onClick={() => {
                  setTaskQuery("");
                  setTaskFilter("all");
                  setSearchOpen(false);
                }}
                title="Close search"
              >
                <X />
                <span className="sr-only">Close search</span>
              </Button>
            </div>
          </div>
        )}
        {timeline.tasks.length === 0 ? (
          <EmptyState
            serverStatus={serverStatus}
            agentType={agentType}
            startup={timeline.prelude}
          />
        ) : (
          <div className="mx-auto w-full max-w-[72rem] px-4 pb-10 pt-3 sm:px-6">
            {timeline.prelude.length > 0 && <StartupScreen messages={timeline.prelude} />}
            {hiddenTaskCount > 0 && (
              <div className="py-4 sm:pl-[4.75rem]">
                <Button type="button" variant="outline" size="sm" onClick={() => setShowAllTasks(true)} className="h-8 rounded-full text-xs">
                  Show {hiddenTaskCount} older {hiddenTaskCount === 1 ? "task" : "tasks"}
                </Button>
              </div>
            )}
            {visibleTasks.length === 0 && filtersActive && (
              <p className="py-10 text-center text-sm text-muted-foreground">
                No tasks match the current search and filter.
              </p>
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
              const isLatest = index === timeline.tasks.length - 1;
              return (
              <React.Fragment key={task.key}>
              {showDateSep && (
                <div className="date-label pt-5 font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground sm:pl-[4.75rem]">
                  {formatDateLabel(currentDate)}
                </div>
              )}
              <TaskGroup
                task={task}
                agentType={agentType}
                number={index + 1}
                status={status}
                onRetryMessage={onRetryMessage}
                onEditMessage={onEditMessage}
                onDismissMessage={onDismissMessage}
                onStopTask={onStopTask}
                onSendRaw={onSendRaw}
                searchQuery={taskQuery}
                searchResultIndex={searchResultIndex}
                isCurrentSearchResult={
                  Boolean(taskQuery) &&
                  searchResultIndex === currentSearchResult
                }
                deferred={
                  !filtersActive &&
                  visibleIndex < visibleTasks.length - 1 &&
                  !renderedTaskKeys.has(task.key)
                }
                waitingForUser={isLatest && Boolean(terminalPrompt)}
              />
              </React.Fragment>
              );
            })}
          </div>
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
