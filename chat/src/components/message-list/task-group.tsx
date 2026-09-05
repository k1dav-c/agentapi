"use client";

import React, { useEffect, useMemo, useState } from "react";
import { CheckCircle2, CircleAlert, Clock3, Clipboard, Download, Eye, LoaderCircle, MoreHorizontal } from "lucide-react";
import { Button } from "../ui/button";
import { ProcessedMessage } from "../processed-message";
import { toast } from "sonner";
import { taskToMarkdown } from "@/lib/task-actions";
import { groupConsecutiveTools } from "@/lib/activity-groups";
import { formatElapsedTime } from "@/lib/format-time";
import { ToolCallCard, ToolCallGroup } from "./tool-call";
import { MessageItem } from "./message-item";
import type { TaskSection, TaskStatus } from "@/lib/task-timeline";
import { getTaskActivity, toSearchableTask } from "@/lib/task-timeline";
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
  searchQuery,
  searchResultIndex,
  isCurrentSearchResult,
  renderMode = "raw",
}: {
  task: TaskSection;
  number: number;
  status: TaskStatus;
  onRetryMessage: (clientId: string) => Promise<boolean>;
  onEditMessage: (clientId: string, content: string) => void;
  onDismissMessage: (clientId: string) => void;
  onStopTask: () => void;
  searchQuery: string;
  searchResultIndex: number;
  isCurrentSearchResult: boolean;
  renderMode?: "raw" | "markdown";
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
      className: "text-status-warning",
    },
    completed: {
      label: "Completed",
      icon: CheckCircle2,
      className: "text-status-success",
    },
    failed: {
      label: "Failed",
      icon: CircleAlert,
      className: "text-destructive",
    },
  }[status];
  const StatusIcon = statusMeta.icon;
  const activity = useMemo(
    () => groupConsecutiveTools(getTaskActivity(task)),
    [task],
  );
  const markdown = taskToMarkdown(toSearchableTask(task), number);
  const [previewOpen, setPreviewOpen] = useState(false);
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
    toast.success(`Task ${number} Markdown downloaded`);
  };

  return (
    <section
      id={`task-${number}`}
      className={`overflow-hidden rounded-2xl border bg-background/70 transition ${
        status === "running"
          ? "border-status-warning/50 shadow-md ring-1 ring-status-warning/15"
          : status === "completed"
            ? "shadow-none"
            : "shadow-sm"
      } ${isCurrentSearchResult ? "ring-2 ring-primary/50" : ""}`}
      data-status={status}
      data-search-result={searchResultIndex}
    >
      <header
        className={`flex min-h-11 items-center justify-between gap-3 border-b px-4 py-2 ${
          status === "running" ? "bg-status-warning/10" : "bg-muted/20"
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
        {activity.map((item) =>
          item.type === "message" ? (
            <MessageItem
              key={item.key}
              message={item.message}
              searchQuery={searchQuery}
              renderMode={renderMode}
            />
          ) : item.type === "tool-group" ? (
            <div key={item.key} className="ml-3 sm:ml-8">
              <ToolCallGroup
                toolCalls={item.toolCalls}
                searchQuery={searchQuery}
              />
            </div>
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
              messageContent={markdown}
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
