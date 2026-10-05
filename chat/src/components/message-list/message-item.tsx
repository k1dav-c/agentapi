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
import type { DraftMessage, Message } from "../chat-provider";

export function CopyButton({
  content,
  label = "response",
  onDark = false,
}: {
  content: string;
  label?: "task" | "response";
  // Sits on a terminal (always dark) surface.
  onDark?: boolean;
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
  searchQuery: globalSearchQuery = "",
}: {
  message: Message | DraftMessage;
  onRetryMessage?: (clientId: string) => Promise<boolean>;
  onEditMessage?: (clientId: string, content: string) => void;
  onDismissMessage?: (clientId: string) => void;
  searchQuery?: string;
}) {
  const isUser = message.role === "user";
  // "assistant" messages come from the agent's session log and are prose;
  // "agent" messages are text parsed from the terminal screen.
  const isTerminal = message.role !== "assistant" && !isUser;
  const isDraft = message.id === undefined;
  const draft = isDraft ? (message as DraftMessage) : undefined;
  const isFailed = draft?.deliveryStatus === "failed";
  const [searchOpen, setSearchOpen] = useState(false);
  const [outputSearchQuery, setOutputSearchQuery] = useState("");
  const [renderMode, setRenderMode] = useState<"raw" | "markdown">(isTerminal ? "raw" : "markdown");
  const effectiveSearchQuery = outputSearchQuery || globalSearchQuery;

  const matchCount =
    outputSearchQuery.trim() === ""
      ? 0
      : message.content
          .toLocaleLowerCase()
          .split(outputSearchQuery.trim().toLocaleLowerCase()).length - 1;

  if (!isUser) {
    const tools = message.content && (
      <div className="float-right ml-3 flex items-center gap-0.5 opacity-0 transition group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100">
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              onClick={() => setRenderMode((mode) => mode === "raw" ? "markdown" : "raw")}
              className={`grid size-7 place-items-center rounded-md outline-none transition focus-visible:ring-2 focus-visible:ring-ring ${isTerminal ? "text-term-dim hover:bg-white/10 hover:text-term-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground"}`}
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
              className={`grid size-7 place-items-center rounded-md outline-none transition focus-visible:ring-2 focus-visible:ring-ring ${isTerminal ? "text-term-dim hover:bg-white/10 hover:text-term-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground"}`}
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
            <ProcessedMessage messageContent={message.content} isUser={false} renderMode={renderMode} searchQuery={effectiveSearchQuery} />
          </div>
        )}
      </article>
    );
  }

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
              <ProcessedMessage messageContent={withoutUploadedImages(message.content)} isUser={isUser} searchQuery={globalSearchQuery} />
            )
          )}
          {message.content && <MessageImages content={message.content} />}
        </div>
      </div>
      {isDraft && !isFailed && <p className="mt-1 text-xs text-muted-foreground">Sending…</p>}
      {isFailed && draft && (
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
