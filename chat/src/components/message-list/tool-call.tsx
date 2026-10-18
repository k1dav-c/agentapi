"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {HighlightedText} from "../highlighted-text";
import { ArrowDown, ChevronRight, FileDiff, FileText, Wrench } from "lucide-react";
import { Button } from "../ui/button";
import { uiCopy } from "@/lib/ui-copy";
import { describeToolCall } from "@/lib/tool-call-view";
import type { ToolCall } from "@/lib/task-timeline";
import { currentPromptQuestion, parseInteractiveToolInput } from "@/lib/interactive-tool";
import {useChatSelector} from "../chat-provider";
import { ImageThumb } from "../image-preview";
import { toolRequestBadge, type Tone } from "@/lib/native-requests";
import { fileName, viewerTab, type FileTarget, type FileTargetKind } from "@/lib/file-refs";
import { useFileViewer, type FileViewer, type FileViewerTab } from "../file-viewer-context";
import { DiffView } from "../diff-view";
import { PathLabel } from "../file-path";
import { CopyButton } from "./message-item";

function formatDuration(ms: number): string {
  if (ms < 1000) return "< 1s";
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

export function ToolDetail({
  label,
  content,
  searchQuery,
  collapsible = false,
}: {
  label: string;
  content: string;
  searchQuery: string;
  // Closed until clicked (or a search matches), for details that are also
  // shown another way.
  collapsible?: boolean;
}) {
  const body = (
    <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md bg-term p-2.5 font-mono text-xs leading-[1.55] text-term-foreground">
      <HighlightedText content={content} query={searchQuery} />
    </pre>
  );
  if (collapsible) {
    return <CollapsibleDetail label={label} searchQuery={searchQuery}>{body}</CollapsibleDetail>;
  }
  return (
    <section>
      <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </h3>
      {body}
    </section>
  );
}

function CollapsibleDetail({label, searchQuery, children}: {label: string; searchQuery: string; children: React.ReactNode}) {
  const [open, setOpen] = useState(Boolean(searchQuery));
  useEffect(() => {
    if (searchQuery) setOpen(true);
  }, [searchQuery]);
  return (
    <details
      className="group/detail"
      open={open}
      onToggle={(event) => {
        // React passes toggle events on to the card's own <details>.
        event.stopPropagation();
        setOpen(event.currentTarget.open);
      }}
    >
      <summary className="flex w-fit cursor-pointer list-none items-center gap-1 rounded-sm text-[11px] font-semibold uppercase tracking-wider text-muted-foreground outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
        <ChevronRight className="size-3 transition-transform group-open/detail:rotate-90" />
        {label}
      </summary>
      <div className="mt-1">{children}</div>
    </details>
  );
}

const KIND_LABEL: Record<FileTargetKind, string> = {
  read: "Read",
  write: "Write",
  edit: "Edit",
  delete: "Delete",
  image: "Image",
};

function openTarget(viewer: FileViewer, target: FileTarget, tab: FileViewerTab = viewerTab(target.kind)) {
  viewer.open({path: target.path, line: target.line, change: target.change, tab});
}

// The files a tool call reads or changes, each with buttons that open it in
// the file viewer on its contents or its changes.
function ToolFiles({targets, viewer}: {targets: FileTarget[]; viewer: FileViewer}) {
  return (
    <section>
      <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        {targets.length === 1 ? "File" : `Files · ${targets.length}`}
      </h3>
      <ul className="divide-y overflow-hidden rounded-md border bg-background">
        {targets.map((target, index) => (
          <li key={`${index}:${target.path}`} className="flex min-h-10 items-center gap-2 py-1 pl-2.5 pr-1 sm:min-h-9">
            <span className="w-12 shrink-0 font-mono text-[10.5px] uppercase tracking-[0.06em] text-muted-foreground">
              {target.movedFrom ? "Move" : KIND_LABEL[target.kind]}
            </span>
            <span
              className="min-w-0 flex-1 text-xs"
              title={target.movedFrom ? `${target.movedFrom} → ${target.path}` : target.path}
            >
              <PathLabel path={target.path} />
            </span>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-8 gap-1 px-2 text-xs sm:h-7"
              aria-label={`File: ${target.path}`}
              aria-haspopup="dialog"
              onClick={() => openTarget(viewer, target, "file")}
            >
              <FileText className="size-3.5 max-sm:hidden" />
              File
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-8 gap-1 px-2 text-xs sm:h-7"
              aria-label={`Diff: ${target.path}`}
              aria-haspopup="dialog"
              onClick={() => openTarget(viewer, target, "diff")}
            >
              <FileDiff className="size-3.5 max-sm:hidden" />
              Diff
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}

// What the tool call changes, as a diff.
function ToolChange({change}: {change: string}) {
  return (
    <section>
      <div className="mb-1 flex items-center justify-between gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Change</h3>
        <CopyButton content={change} label="diff" />
      </div>
      <DiffView diff={change} label="Change" className="max-h-80" />
    </section>
  );
}

function ToolChip({children, tone}: {children: React.ReactNode; tone?: Tone}) {
  return (
    <span
      className={`shrink-0 rounded-[4px] border px-1.5 font-mono text-[11px] leading-[18px] tabular-nums ${
        tone === "ok"
          ? "border-state-ready/40 text-state-ready"
          : tone === "fault"
            ? "border-state-fault/40 text-state-fault"
            : tone === "needs"
              ? "border-state-needs/50 text-state-needs"
              : "text-muted-foreground"
      }`}
    >
      {children}
    </span>
  );
}

// A tool call: its header (a command, a file, or the tool and a summary,
// with its state), and its details when open. Memoized: a tool call that
// didn't change keeps its object (shareToolCalls), and the card renders
// again only when it, the search or its request changes.
export const ToolCallCard = React.memo(function ToolCallCard({
  toolCall,
  searchQuery = "",
  open,
  onOpenChange,
  onSendRaw,
}: {
  toolCall: ToolCall;
  searchQuery?: string;
  open?: boolean;
  // The group's open state; the card is controlled when open is set.
  onOpenChange?: (id: string, open: boolean) => void;
  onSendRaw?: (data: string) => void;
}) {
  const view = useMemo(() => describeToolCall(toolCall), [toolCall]);
  const {targets, change, command, exitCode, durationMs, images, imageName, input} = view;
  const isFailed = view.failed;
  const isPending = view.pending;
  const storageScope = useChatSelector((chat) => chat.storageScope);
  const features = useChatSelector((chat) => chat.features);
  const requests = useChatSelector((chat) => chat.requests);
  // A question in a tool call is answered here with keystrokes, so only
  // where they reach the agent's terminal (PTY). On the native transport it
  // is answered on its request card, and the tool call shows the request's
  // state; ACP has neither.
  const interactiveInput = isPending && onSendRaw ? parseInteractiveToolInput(toolCall.name, toolCall.input) : null;
  const request = features.requests ? requests.findLast((candidate) => candidate.tool_use_id === toolCall.id) : undefined;
  const requestBadge = request ? toolRequestBadge(request) : null;
  const [localIsOpen, setLocalIsOpen] = useState(Boolean(searchQuery) || Boolean(interactiveInput));
  // The agent's prompt shows one question at a time and moves on as each is
  // answered, so only the question it is asking accepts clicks. Codex prints
  // which one that is; otherwise count the answers given here.
  const [answeredCount, setAnsweredCount] = useState(0);
  // Only a card with questions reads the messages, so the others don't
  // render again as they stream.
  const lastAgentOutput = useChatSelector((chat) =>
    interactiveInput ? (chat.messages.findLast((message) => message.role === "agent")?.content ?? "") : "",
  );
  const activeQuestion = currentPromptQuestion(lastAgentOutput) ?? answeredCount;
  const isOpen = open ?? localIsOpen;

  useEffect(() => {
    if (open === undefined && searchQuery) {
      setLocalIsOpen(true);
    }
  }, [open, searchQuery]);

  const viewer = useFileViewer();
  // A command's first lines of output show while the card is closed.
  const preview = isOpen ? "" : view.resultPreview;

  return (
    <details
      className="group overflow-hidden rounded-md border bg-muted/35"
      open={isOpen}
      onToggle={(event) => {
        if (open === undefined) {
          setLocalIsOpen(event.currentTarget.open);
        } else {
          onOpenChange?.(toolCall.id, event.currentTarget.open);
        }
      }}
    >
      <summary className="block cursor-pointer list-none outline-none transition hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
        <span className="flex min-h-8 items-center gap-2.5 px-2.5 py-1 font-mono text-[12.5px] leading-5">
          {isPending ? (
            <span
              aria-label={request?.status === "pending" ? "Waiting for you" : "Running"}
              className={`size-1.5 shrink-0 rounded-full motion-safe:animate-pulse ${request?.status === "pending" ? "bg-state-needs" : "bg-state-working"}`}
            />
          ) : null}
          {command ? (
            <span className="min-w-0 flex-1 truncate">
              <span className="mr-2 text-muted-foreground" aria-hidden="true">$</span>
              {command.split("\n")[0]}
            </span>
          ) : targets.length > 0 && viewer ? (
            <span className="flex min-w-0 flex-1 items-baseline gap-2">
              <span className="shrink-0 font-sans text-xs font-semibold">{view.label}</span>
              <button
                type="button"
                className="min-w-0 truncate rounded-sm text-muted-foreground underline decoration-muted-foreground/40 decoration-dotted underline-offset-[3px] outline-none transition hover:text-foreground hover:decoration-solid focus-visible:ring-2 focus-visible:ring-ring"
                title={targets[0].path}
                aria-label={`Open ${targets[0].path}`}
                aria-haspopup="dialog"
                onClick={(event) => {
                  // Opens the file, not the card.
                  event.preventDefault();
                  event.stopPropagation();
                  openTarget(viewer, targets[0]);
                }}
              >
                {fileName(targets[0].path)}
              </button>
              {targets.length > 1 && (
                <span className="shrink-0 text-muted-foreground">
                  <span aria-hidden="true">+{targets.length - 1}</span>
                  <span className="sr-only">and {targets.length - 1} more {targets.length === 2 ? "file" : "files"}</span>
                </span>
              )}
            </span>
          ) : (
            <span className="min-w-0 flex-1 truncate">
              <span className="font-sans text-xs font-semibold">{toolCall.name}</span>
              {view.summary && <span className="ml-2 text-muted-foreground">{view.summary}</span>}
            </span>
          )}
          {requestBadge && (
            <ToolChip tone={requestBadge.tone}>
              <span data-request-state={request?.status}>{requestBadge.label}</span>
            </ToolChip>
          )}
          {exitCode !== undefined ? (
            <ToolChip tone={exitCode === 0 ? "ok" : "fault"}>exit {exitCode}</ToolChip>
          ) : isFailed ? (
            <ToolChip tone="fault">error</ToolChip>
          ) : null}
          {durationMs > 0 && <ToolChip>{formatDuration(durationMs)}</ToolChip>}
          <ArrowDown className="size-3 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
        </span>
        {preview && (
          <pre className="max-h-[5.5rem] overflow-hidden whitespace-pre-wrap break-words bg-term px-3 py-2 font-mono text-xs leading-[1.55] text-term-foreground [overflow-wrap:anywhere]">
            {preview}
          </pre>
        )}
        {images.length > 0 && (
          // Shown while the card is closed too. A click opens the image,
          // not the card.
          <span className="flex flex-wrap gap-2 px-2.5 pb-2" onClick={(event) => event.preventDefault()}>
            {images.map((url, index) => (
              <ImageThumb
                key={url}
                src={`${storageScope}${url}`}
                name={images.length > 1 ? `${imageName} (${index + 1})` : imageName}
                className="size-24"
              />
            ))}
          </span>
        )}
      </summary>
      <div className="space-y-3 border-t px-3 py-3">
        {interactiveInput && interactiveInput.questions.length > 0 ? (
          interactiveInput.questions.map((q, qi) => (
            <div key={qi} className={`space-y-2 ${qi === activeQuestion ? "" : "opacity-60"}`}>
              <p className="text-xs font-medium text-foreground">{q.question}</p>
              <div className="flex flex-wrap gap-1.5">
                {(q.options ?? []).map((opt, oi) => (
                  <button
                    key={oi}
                    type="button"
                    disabled={qi !== activeQuestion}
                    onClick={() => {
                      // Both Claude Code and Codex pick an option as soon as
                      // its digit is typed; an extra Enter would answer the
                      // next question with its default.
                      onSendRaw?.(String(oi + 1));
                      setAnsweredCount(qi + 1);
                    }}
                    className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground shadow-sm transition hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none"
                    title={opt.description}
                  >
                    <span className="grid size-5 place-items-center rounded bg-muted text-[10px] font-bold">{oi + 1}</span>
                    <span className="max-w-52 truncate">{opt.label}</span>
                  </button>
                ))}
              </div>
              {qi === activeQuestion && (
                <p className="text-[11px] text-muted-foreground">
                  Or type your own answer in the message box.
                </p>
              )}
              {q.options?.some(o => o.description) && (
                <div className="space-y-1 pt-1">
                  {q.options.map((opt, oi) => opt.description ? (
                    <p key={oi} className="text-[11px] text-muted-foreground">
                      <span className="font-medium">{oi + 1}.</span> {opt.label} — {opt.description}
                    </p>
                  ) : null)}
                </div>
              )}
            </div>
          ))
        ) : (
          <>
            {/* Only while open: a closed card's body is in the page too,
                and a big write's diff is a row per line. */}
            {isOpen && targets.length > 0 && viewer && <ToolFiles targets={targets} viewer={viewer} />}
            {isOpen && change && <ToolChange change={change} />}
            {input && (
              // With the change shown, the raw input stays a click away.
              <ToolDetail label="Input" content={input} searchQuery={searchQuery} collapsible={Boolean(change)} />
            )}
          </>
        )}
        {toolCall.result !== undefined && (
          <ToolDetail
            label={isFailed ? "Error" : "Result"}
            content={toolCall.result || (images.length > 0 ? "(Image)" : "(No output)")}
            searchQuery={searchQuery}
          />
        )}
        {!input && !interactiveInput && toolCall.result === undefined && (
          <p className="text-xs text-muted-foreground">
            No tool details are available yet.
          </p>
        )}
      </div>
    </details>
  );
});

export function ToolCallGroup({
  toolCalls,
  searchQuery,
  onSendRaw,
}: {
  toolCalls: ToolCall[];
  searchQuery: string;
  onSendRaw?: (data: string) => void;
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
  // One handler for every card, so the memoized cards don't render again.
  const setToolOpen = useCallback((id: string, open: boolean) => {
    setOpenToolIDs((current) => {
      const next = new Set(current);
      if (open) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);

  return (
    <section className="space-y-1.5">
      <header className="flex items-center justify-between gap-2">
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
      <div className="space-y-1.5">
        {toolCalls.map((toolCall) => (
          <ToolCallCard
            key={toolCall.id}
            toolCall={toolCall}
            searchQuery={searchQuery}
            onSendRaw={onSendRaw}
            open={openToolIDs.has(toolCall.id)}
            onOpenChange={setToolOpen}
          />
        ))}
      </div>
    </section>
  );
}
