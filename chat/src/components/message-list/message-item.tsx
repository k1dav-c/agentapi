"use client";

import React, { useState } from "react";
import { Check, Clipboard, Code2, FileText, Pencil, RefreshCw, Search, TerminalSquare, UserRound, X } from "lucide-react";
import { Button } from "../ui/button";
import { Tooltip, TooltipTrigger, TooltipContent } from "../ui/tooltip";
import { ProcessedMessage } from "../processed-message";
import { MessageImages } from "../image-preview";
import { withoutUploadedImages } from "@/lib/uploads";
import { toast } from "sonner";
import { formatMessageTime } from "@/lib/format-time";
import { copyText } from "@/lib/clipboard";
import {isDraftMessage, type DraftMessage, type Message} from "@/lib/conversation";

export function CopyButton({
  content,
  label = "response",
  onDark = false,
}: {
  content: string;
  label?: "task" | "response" | "command" | "path" | "diff";
  // Sits on a terminal (always dark) surface.
  onDark?: boolean;
}) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await copyText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard access can be blocked in embedded contexts.
      setCopied(false);
      toast.error(`Could not copy the ${label}`, {
        description: "Clipboard access may be blocked in this browser context.",
      });
    }
  };

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={copy}
          className={`grid size-9 shrink-0 place-items-center rounded-md outline-none transition focus-visible:ring-2 focus-visible:ring-ring sm:size-7 ${onDark ? "text-term-dim hover:bg-white/10 hover:text-term-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground"}`}
          aria-label={copied ? `Copied ${label}` : `Copy ${label}`}
        >
          {copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}
        </button>
      </TooltipTrigger>
      <TooltipContent>{copied ? `Copied ${label}` : `Copy ${label}`}</TooltipContent>
    </Tooltip>
  );
}

export const LoadingDots = () => (
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

export function MessageItem({
  message,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  searchQuery = "",
}: {
  message: Message | DraftMessage;
  onRetryMessage?: (clientId: string) => Promise<boolean>;
  onEditMessage?: (clientId: string, content: string) => void;
  onDismissMessage?: (clientId: string) => void;
  searchQuery?: string;
}) {
  if (message.role !== "user") return <AgentMessage message={message} searchQuery={searchQuery} />;
  return (
    <UserMessage
      message={message}
      onRetryMessage={onRetryMessage}
      onEditMessage={onEditMessage}
      onDismissMessage={onDismissMessage}
      searchQuery={searchQuery}
    />
  );
}

// The look of the small buttons on a block of the agent's output.
function blockButtonClass(onDark: boolean) {
  return `grid size-7 place-items-center rounded-md outline-none transition focus-visible:ring-2 focus-visible:ring-ring ${
    onDark ? "text-term-dim hover:bg-white/10 hover:text-term-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground"
  }`;
}

// A message of the agent's: prose from its session log ("assistant"), or
// text parsed from its terminal screen ("agent"), with buttons to show it
// raw or as Markdown, search it and copy it.
function AgentMessage({message, searchQuery}: {message: Message | DraftMessage; searchQuery: string}) {
  const isTerminal = message.role !== "assistant";
  const isDraft = message.id === undefined;
  const [searchOpen, setSearchOpen] = useState(false);
  const [outputSearchQuery, setOutputSearchQuery] = useState("");
  const [renderMode, setRenderMode] = useState<"raw" | "markdown">(isTerminal ? "raw" : "markdown");
  const effectiveSearchQuery = outputSearchQuery || searchQuery;
  const matchCount =
    outputSearchQuery.trim() === ""
      ? 0
      : message.content
          .toLocaleLowerCase()
          .split(outputSearchQuery.trim().toLocaleLowerCase()).length - 1;

  const tools = message.content && (
    <div className="float-right ml-3 flex items-center gap-0.5 opacity-0 transition group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100">
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={() => setRenderMode((mode) => mode === "raw" ? "markdown" : "raw")}
            className={blockButtonClass(isTerminal)}
            aria-label={renderMode === "markdown" ? "Show this block as raw text" : "Show this block as Markdown"}
            aria-pressed={renderMode === "markdown"}
          >
            {renderMode === "markdown" ? <Code2 className="size-3.5" /> : <FileText className="size-3.5" />}
          </button>
        </TooltipTrigger>
        <TooltipContent>{renderMode === "markdown" ? "Show raw" : "Show Markdown"}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={() => {
              setSearchOpen((open) => !open);
              if (searchOpen) setOutputSearchQuery("");
            }}
            className={blockButtonClass(isTerminal)}
            aria-label="Search this block"
            aria-expanded={searchOpen}
          >
            <Search className="size-3.5" />
          </button>
        </TooltipTrigger>
        <TooltipContent>Search this block</TooltipContent>
      </Tooltip>
      <CopyButton content={message.content} onDark={isTerminal} />
    </div>
  );
  return (
    <article className="group min-w-0">
      {searchOpen && (
        <label className="mb-2 flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-1.5 text-xs">
          <Search className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="sr-only">Search this block</span>
          <input
            autoFocus
            type="search"
            value={outputSearchQuery}
            onChange={(event) => setOutputSearchQuery(event.target.value)}
            placeholder="Search this block…"
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
      ) : isTerminal ? (
        <div className="rounded-md bg-term px-3.5 py-2.5 text-term-foreground">
          {tools}
          <div className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.06em] text-term-dim">
            <TerminalSquare className="size-3" />
            Terminal
            {message.time && (
              <time dateTime={message.time} className="normal-case tracking-normal" title={new Date(message.time).toLocaleString()}>
                · {formatMessageTime(message.time)}
              </time>
            )}
            {isDraft && <span className="normal-case tracking-normal">· updating…</span>}
          </div>
          <ProcessedMessage messageContent={message.content} isUser={false} renderMode={renderMode} searchQuery={effectiveSearchQuery} />
        </div>
      ) : (
        <div>
          {tools}
          <ProcessedMessage messageContent={message.content} isUser={false} renderMode={renderMode} searchQuery={effectiveSearchQuery} linkFilePaths />
        </div>
      )}
    </article>
  );
}

// A message of yours: the task, its images, and while it is a draft that
// failed to send, what to do about it.
function UserMessage({
  message,
  onRetryMessage,
  onEditMessage,
  onDismissMessage,
  searchQuery,
}: {
  message: Message | DraftMessage;
  onRetryMessage?: (clientId: string) => Promise<boolean>;
  onEditMessage?: (clientId: string, content: string) => void;
  onDismissMessage?: (clientId: string) => void;
  searchQuery: string;
}) {
  const draft = isDraftMessage(message) ? message : undefined;
  const isFailed = draft?.deliveryStatus === "failed";
  return (
    <article className="group scroll-mt-16" data-user-message aria-label="Your message">
      <div className="rounded-xl border border-prompt-border bg-prompt px-4 pb-3 pt-2.5">
        <div className="mb-1 flex min-h-7 items-center gap-2">
          <span className="flex items-center gap-1.5 text-xs font-semibold text-foreground">
            <UserRound aria-hidden="true" className="size-3.5 text-muted-foreground" />
            You
          </span>
          {message.content && (
            <div className="ml-auto opacity-0 transition group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100">
              <CopyButton content={message.content} label="task" />
            </div>
          )}
        </div>
        <div className="prompt-text min-w-0 text-foreground">
          {message.content === "" ? (
            <LoadingDots />
          ) : (
            // Uploaded images show as thumbnails below, not as their paths.
            withoutUploadedImages(message.content) && (
              <ProcessedMessage messageContent={withoutUploadedImages(message.content)} isUser searchQuery={searchQuery} />
            )
          )}
          {message.content && <MessageImages content={message.content} />}
        </div>
      </div>
      {draft && !isFailed && <p className="mt-1 text-xs text-muted-foreground">Sending…</p>}
      {draft && isFailed && (
        <div
          className="mt-2 flex flex-wrap items-center gap-1"
          role="alert"
          aria-label={`Message was not sent${draft.error ? `: ${draft.error}` : ""}`}
        >
          <span className="mr-1 text-xs text-state-fault">Not sent{draft.error ? `: ${draft.error}` : ""}</span>
          <Button type="button" size="sm" variant="outline" className="h-7" onClick={() => void onRetryMessage?.(draft.clientId)}>
            <RefreshCw />
            Retry
          </Button>
          <Button type="button" size="sm" variant="ghost" className="h-7" onClick={() => onEditMessage?.(draft.clientId, draft.content)}>
            <Pencil />
            Edit
          </Button>
          <Button type="button" size="icon" variant="ghost" className="size-7" onClick={() => onDismissMessage?.(draft.clientId)} title="Dismiss failed message">
            <X />
            <span className="sr-only">Dismiss failed message</span>
          </Button>
        </div>
      )}
    </article>
  );
}
