"use client";

import React, {useCallback, useEffect, useMemo, useRef, useState} from "react";
import {Search} from "lucide-react";
import {createPortal} from "react-dom";
import {Button} from "./ui/button";
import {AgentType} from "@/lib/agents";
import type {DraftMessage, Message, RichMessage} from "@/lib/conversation";
import type {ServerStatus} from "@/lib/agent-status";
import {formatDateLabel} from "@/lib/format-time";
import {filterTasks, timelineSignature} from "@/lib/task-filter";
import {type Timeline, type ToolCall, buildTimeline, collectToolCalls, shareTimeline, shareToolCalls} from "@/lib/task-timeline";
import {HEADER_ACTIONS_ID} from "@/app/header";
import {useChat} from "./chat-provider";
import {TaskGroup} from "./message-list/task-group";
import {EmptyState, StartupScreen} from "./message-list/empty-state";
import {JumpButtons} from "./message-list/jump-buttons";
import {TaskSearchBar} from "./message-list/task-search-bar";
import {useProgressiveTasks} from "./message-list/use-progressive-tasks";
import {useTaskSearch} from "./message-list/use-task-search";
import {useTranscriptScroll} from "./message-list/use-transcript-scroll";

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
  // Stops the running task; unset when it can't be stopped.
  onStopTask?: () => void;
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
  const [showAllTasks, setShowAllTasks] = useState(false);
  const search = useTaskSearch();
  const {taskQuery, taskFilter, filtersActive, currentSearchResult, setCurrentSearchResult, searchOpen} = search;
  const {waitingFor} = useChat();
  // The page's actions render into the header's slot (one top bar); the
  // embed page has no header, so they get their own row there.
  const [headerSlot, setHeaderSlot] = useState<HTMLElement | null>(null);
  useEffect(() => setHeaderSlot(document.getElementById(HEADER_ACTIONS_ID)), []);
  // Rebuilt as messages stream; unchanged tasks and tool calls keep their
  // objects (shareTimeline), so their memoized components don't render
  // again.
  const sharedCalls = useRef<ToolCall[]>([]);
  const toolCalls = useMemo(
    () => (sharedCalls.current = shareToolCalls(sharedCalls.current, collectToolCalls(richMessages))),
    [richMessages],
  );
  const sharedTimeline = useRef<Timeline | null>(null);
  const timeline = useMemo(
    () => (sharedTimeline.current = shareTimeline(sharedTimeline.current, buildTimeline(messages, richMessages, toolCalls))),
    [messages, richMessages, toolCalls],
  );
  const filteredTasks = useMemo(
    () => filterTasks(timeline.tasks, serverStatus, taskFilter, taskQuery),
    [serverStatus, taskFilter, taskQuery, timeline.tasks],
  );
  const totalMatchCount = filteredTasks.reduce((total, task) => total + task.matchCount, 0);
  const hiddenTaskCount =
    !filtersActive && !showAllTasks && filteredTasks.length > VISIBLE_TASKS_DEFAULT
      ? filteredTasks.length - VISIBLE_TASKS_DEFAULT
      : 0;
  const visibleTasks = hiddenTaskCount > 0 ? filteredTasks.slice(hiddenTaskCount) : filteredTasks;
  const renderedTaskKeys = useProgressiveTasks(visibleTasks.slice(0, -1).map(({task}) => task.key), filtersActive);
  const signature = useMemo(() => timelineSignature(timeline), [timeline]);
  const isEmpty = timeline.tasks.length === 0;
  const scroll = useTranscriptScroll(scrollArea, {
    messages,
    signature,
    serverStatus,
    layout: `${isEmpty}:${searchOpen}:${filtersActive}`,
  });

  const navigateSearchResults = useCallback(
    (direction: -1 | 1) => {
      if (!scrollArea || filteredTasks.length === 0) return;
      const next = (currentSearchResult + direction + filteredTasks.length) % filteredTasks.length;
      setCurrentSearchResult(next);
      scrollArea
        .querySelector<HTMLElement>(`[data-search-result="${next}"]`)
        ?.scrollIntoView({behavior: "smooth", block: "center"});
    },
    [currentSearchResult, filteredTasks.length, scrollArea, setCurrentSearchResult],
  );

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
        onClick={() => search.setSearchOpen((open) => !open)}
      >
        <Search className="size-4" />
      </Button>
      {headerAction}
    </>
  );

  return (
    // Clipped, so the floating buttons can't spill over the header.
    <div className="relative min-h-0 flex-1 overflow-hidden">
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
          <TaskSearchBar
            search={search}
            shown={filteredTasks.length}
            total={timeline.tasks.length}
            matches={totalMatchCount}
            onNavigate={navigateSearchResults}
          />
        )}
        {timeline.tasks.length === 0 ? (
          <EmptyState
            serverStatus={serverStatus}
            agentType={agentType}
            startup={timeline.prelude}
          />
        ) : (
          <div className="mx-auto w-full max-w-[49rem] px-4 pb-10 pt-3 sm:px-6">
            {/* One reading column, centered: 46rem of text inside the padding.
                The time rail and thinking notes sit in the margins beside it. */}
            {timeline.prelude.length > 0 && <StartupScreen messages={timeline.prelude} />}
            {hiddenTaskCount > 0 && (
              <div className="py-4">
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
                <div className="date-label pt-5 font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">
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
                waitingForUser={isLatest && waitingFor === "answer"}
              />
              </React.Fragment>
              );
            })}
          </div>
        )}
      </div>

      <JumpButtons scroll={scroll} />
    </div>
  );
}
