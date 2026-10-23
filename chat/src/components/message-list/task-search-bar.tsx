"use client";

import {ArrowLeft, ArrowRight, Search, X} from "lucide-react";
import {Button} from "../ui/button";
import {isImeComposing} from "@/lib/keys";
import {uiCopy} from "@/lib/ui-copy";
import type {TaskFilter} from "@/lib/task-timeline";
import type {useTaskSearch} from "./use-task-search";

// The transcript's search bar, stuck to its top: the query, the filter,
// how many tasks show (and matches), moving between the matching tasks.
export function TaskSearchBar({
  search,
  shown,
  total,
  matches,
  onNavigate,
}: {
  search: ReturnType<typeof useTaskSearch>;
  shown: number;
  total: number;
  matches: number;
  onNavigate: (direction: -1 | 1) => void;
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
            <option value="all">{uiCopy.taskToolbar.all}</option>
            <option value="running">{uiCopy.taskToolbar.running}</option>
            <option value="queued">{uiCopy.taskToolbar.queued}</option>
            <option value="failed">{uiCopy.taskToolbar.failed}</option>
            <option value="limited">{uiCopy.taskToolbar.limited}</option>
            <option value="completed">{uiCopy.taskToolbar.completed}</option>
            <option value="tool-error">{uiCopy.taskToolbar.toolErrors}</option>
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
        <Button type="button" size="icon" variant="ghost" className="size-8" onClick={search.close} title="Close search">
          <X />
          <span className="sr-only">Close search</span>
        </Button>
      </div>
    </div>
  );
}
