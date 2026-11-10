"use client";

import {ArrowLeft, ArrowRight, Search, X} from "lucide-react";
import {Button} from "../ui/button";
import {isImeComposing} from "@/lib/keys";
import {TASK_STATUS_META, type TaskFilter, type TaskStatus} from "@/lib/task-timeline";
import type {useTaskSearch} from "./use-task-search";

// The states the filter offers, in its order.
const FILTER_STATUSES: TaskStatus[] = ["running", "queued", "failed", "limited", "completed"];

// The transcript's search bar, stuck to its top: the query, the filter,
// how many tasks show (and matches), moving between the matching tasks.
export function TaskSearchBar({
  search,
  shown,
  total,
  matches,
  onNavigate,
  earlier = 0,
  loadingEarlier = false,
  onLoadEarlier,
}: {
  search: ReturnType<typeof useTaskSearch>;
  shown: number;
  total: number;
  matches: number;
  onNavigate: (direction: -1 | 1) => void;
  // The search goes through the tasks the chat holds: earlier ones the
  // server keeps (lib/history.ts) are searched once loaded.
  earlier?: number;
  loadingEarlier?: boolean;
  onLoadEarlier?: () => void;
}) {
  const {taskQuery, setTaskQuery, taskFilter, setTaskFilter, searchInputRef} = search;
  return (
    <div className="sticky top-0 z-10 border-b bg-background/95 px-3 py-1.5 backdrop-blur-xl sm:px-6">
      <div className="mx-auto flex w-full max-w-[46rem] flex-wrap items-center gap-1.5 sm:gap-2">
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
              if (event.key === "Enter" && taskQuery && !isImeComposing(event)) {
                event.preventDefault();
                onNavigate(event.shiftKey ? -1 : 1);
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
            <option value="all">All tasks</option>
            {/* The states as the tasks' badges name them. */}
            {FILTER_STATUSES.map((status) => (
              <option key={status} value={status}>{TASK_STATUS_META[status].label}</option>
            ))}
            <option value="tool-error">Tool errors</option>
          </select>
        </label>
        <span className="text-xs tabular-nums text-muted-foreground" role="status">
          {taskQuery ? `${shown} tasks · ${matches} matches` : `${shown} of ${total}`}
        </span>
        {taskQuery && shown > 0 && (
          <div className="flex overflow-hidden rounded-md border bg-card">
            <Button type="button" size="icon" variant="ghost" className="size-8 rounded-none" onClick={() => onNavigate(-1)} title="Previous matching task">
              <ArrowLeft />
              <span className="sr-only">Previous matching task</span>
            </Button>
            <Button type="button" size="icon" variant="ghost" className="size-8 rounded-none border-l" onClick={() => onNavigate(1)} title="Next matching task">
              <ArrowRight />
              <span className="sr-only">Next matching task</span>
            </Button>
          </div>
        )}
        {earlier > 0 && onLoadEarlier && (
          <Button type="button" size="sm" variant="ghost" className="h-8 px-2 text-xs" onClick={onLoadEarlier} disabled={loadingEarlier} title="Load the earlier tasks to search them too">
            {loadingEarlier ? "Loading…" : `+${earlier} earlier`}
          </Button>
        )}
        <Button type="button" size="icon" variant="ghost" className="size-8" onClick={search.close} title="Close search">
          <X />
          <span className="sr-only">Close search</span>
        </Button>
      </div>
    </div>
  );
}
