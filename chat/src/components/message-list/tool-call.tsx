"use client";

import React, { useEffect, useState } from "react";
import { ArrowDown, CheckCircle2, CircleAlert, LoaderCircle, Wrench } from "lucide-react";
import { Button } from "../ui/button";
import { uiCopy } from "@/lib/ui-copy";
import { formatToolInput, getToolSummary } from "@/lib/tool-format";
import type { ToolCall } from "@/lib/task-timeline";

function formatDuration(ms: number): string {
  if (ms < 1000) return "< 1s";
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

export function HighlightedText({
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
        className="rounded-sm bg-search-highlight px-0.5 text-search-highlight-text"
      >
        {part}
      </mark>
    ) : (
      <React.Fragment key={`${index}-${part}`}>{part}</React.Fragment>
    ),
  );
}

export function ToolDetail({
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
      <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </h3>
      <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-background p-2 font-mono text-xs leading-5">
        <HighlightedText content={content} query={searchQuery} />
      </pre>
    </section>
  );
}

export function ToolCallCard({
  toolCall,
  searchQuery = "",
  open,
  onOpenChange,
}: {
  toolCall: ToolCall;
  searchQuery?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const isFailed = toolCall.status === "failed" || Boolean(toolCall.isError);
  const isPending =
    toolCall.status === "running" ||
    (toolCall.status === undefined && toolCall.result === undefined);
  const input = formatToolInput(toolCall.input);
  const [localIsOpen, setLocalIsOpen] = useState(Boolean(searchQuery));
  const isOpen = open ?? localIsOpen;

  useEffect(() => {
    if (open === undefined && searchQuery) {
      setLocalIsOpen(true);
    }
  }, [open, searchQuery]);

  return (
    <details
      className="group overflow-hidden rounded-lg border-l-2 border-y-0 border-r-0 bg-muted/20"
      open={isOpen}
      onToggle={(event) => {
        if (open === undefined) {
          setLocalIsOpen(event.currentTarget.open);
        } else {
          onOpenChange?.(event.currentTarget.open);
        }
      }}
    >
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-1.5 transition hover:bg-muted/45 [&::-webkit-details-marker]:hidden">
        {isFailed ? (
          <CircleAlert className="size-3.5 shrink-0 text-destructive" />
        ) : isPending ? (
          <LoaderCircle className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
        ) : (
          <CheckCircle2 className="size-3.5 shrink-0 text-status-success" />
        )}
        <span className="min-w-0 flex-1 truncate text-xs font-medium">
          {toolCall.name}
          {(() => {
            const summary = getToolSummary(toolCall.name, toolCall.input);
            return summary ? <span className="ml-1.5 font-normal text-muted-foreground">— {summary}</span> : null;
          })()}
        </span>
        {toolCall.resultTimestamp && toolCall.timestamp && (() => {
          const ms = Date.parse(toolCall.resultTimestamp) - Date.parse(toolCall.timestamp);
          return ms > 0 ? (
            <span className="shrink-0 tabular-nums text-[10px] text-muted-foreground">{formatDuration(ms)}</span>
          ) : null;
        })()}
        <ArrowDown className="size-3 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
      </summary>
      <div className="space-y-3 border-t bg-muted/20 px-3 py-3">
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

export function ToolCallGroup({
  toolCalls,
  searchQuery,
}: {
  toolCalls: ToolCall[];
  searchQuery: string;
}) {
  const [openToolIDs, setOpenToolIDs] = useState<Set<string>>(
    () =>
      new Set(searchQuery ? toolCalls.map((tool) => tool.id) : []),
  );
  const allOpen = toolCalls.every((tool) => openToolIDs.has(tool.id));

  useEffect(() => {
    if (searchQuery) {
      setOpenToolIDs(new Set(toolCalls.map((tool) => tool.id)));
    }
  }, [searchQuery, toolCalls]);

  return (
    <section className="overflow-hidden rounded-xl border bg-muted/10">
      <header className="flex items-center justify-between gap-2 border-b bg-muted/25 px-3 py-1">
        <span className="flex min-w-0 items-center gap-1.5 text-xs font-medium">
          <Wrench className="size-3 shrink-0 text-muted-foreground" />
          <span>{uiCopy.tools.groupLabel(toolCalls.length)}</span>
        </span>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-6 shrink-0 px-1.5 text-xs"
          onClick={() =>
            setOpenToolIDs(
              allOpen ? new Set() : new Set(toolCalls.map((tool) => tool.id)),
            )
          }
          aria-expanded={allOpen}
        >
          {allOpen ? uiCopy.tools.collapseAll : uiCopy.tools.expandAll}
        </Button>
      </header>
      <div className="space-y-1 p-1">
        {toolCalls.map((toolCall) => (
          <ToolCallCard
            key={toolCall.id}
            toolCall={toolCall}
            searchQuery={searchQuery}
            open={openToolIDs.has(toolCall.id)}
            onOpenChange={(open) =>
              setOpenToolIDs((current) => {
                const next = new Set(current);
                if (open) next.add(toolCall.id);
                else next.delete(toolCall.id);
                return next;
              })
            }
          />
        ))}
      </div>
    </section>
  );
}
