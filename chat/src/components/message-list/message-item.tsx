"use client";

import React, { useState } from "react";
import { Check, Clipboard, Pencil, RefreshCw, Search, TerminalSquare, User, X } from "lucide-react";
import { Button } from "../ui/button";
import { Tooltip, TooltipTrigger, TooltipContent } from "../ui/tooltip";
import { ProcessedMessage } from "../processed-message";
import { toast } from "sonner";
import { formatMessageTime } from "@/lib/format-time";
import type { DraftMessage, Message } from "../chat-provider";

export function CopyButton({
  content,
  label = "response",
}: {
  content: string;
  label?: "task" | "response";
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
          className="grid size-11 shrink-0 place-items-center rounded-md text-muted-foreground outline-none transition hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring sm:size-9"
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
  renderMode = "raw",
}: {
  message: Message | DraftMessage;
  onRetryMessage?: (clientId: string) => Promise<boolean>;
  onEditMessage?: (clientId: string, content: string) => void;
  onDismissMessage?: (clientId: string) => void;
  searchQuery?: string;
  renderMode?: "raw" | "markdown";
}) {
  const isUser = message.role === "user";
  const isDraft = message.id === undefined;
  const draft = isDraft ? (message as DraftMessage) : undefined;
  const isFailed = draft?.deliveryStatus === "failed";
  const [searchOpen, setSearchOpen] = useState(false);
  const [outputSearchQuery, setOutputSearchQuery] = useState("");
  const effectiveSearchQuery = outputSearchQuery || globalSearchQuery;
  const matchCount =
    outputSearchQuery.trim() === ""
      ? 0
      : message.content
          .toLocaleLowerCase()
          .split(outputSearchQuery.trim().toLocaleLowerCase()).length - 1;

  if (!isUser) {
    return (
      <article className="min-w-0">
        <div className="mb-2 flex min-h-9 flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
            <TerminalSquare className="size-3.5" />
            <span>Agent output</span>
            {message.time && (
              <time
                dateTime={message.time}
                className="font-normal normal-case tracking-normal"
                title={new Date(message.time).toLocaleString()}
              >
                {formatMessageTime(message.time)}
              </time>
            )}
            {isDraft && (
              <span className="normal-case tracking-normal">Updating…</span>
            )}
          </div>
          {message.content && (
            <div className="flex items-center gap-1">
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={() => {
                      setSearchOpen((open) => !open);
                      if (searchOpen) setOutputSearchQuery("");
                    }}
                    className="grid size-9 place-items-center rounded-md text-muted-foreground outline-none transition hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                    aria-label="Search output"
                    aria-expanded={searchOpen}
                  >
                    <Search className="size-3.5" />
                  </button>
                </TooltipTrigger>
                <TooltipContent>Search output</TooltipContent>
              </Tooltip>
              <CopyButton content={message.content} />
            </div>
          )}
        </div>
        {searchOpen && (
          <label className="mb-2 flex items-center gap-2 rounded-lg border bg-muted/20 px-3 py-2 text-xs">
            <Search className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="sr-only">Search this agent output</span>
            <input
              autoFocus
              type="search"
              value={outputSearchQuery}
              onChange={(event) => setOutputSearchQuery(event.target.value)}
              placeholder="Search this output…"
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
        ) : (
          <div className="rounded-xl border bg-card/50 px-4 py-3">
            <ProcessedMessage
              messageContent={message.content}
              isUser={false}
              renderMode={renderMode}
              searchQuery={effectiveSearchQuery}
            />
          </div>
        )}
      </article>
    );
  }

  return (
    <article
      className="flex scroll-mt-4 flex-row-reverse gap-3 border-t pt-7 first:border-t-0 first:pt-0 sm:gap-4"
      data-user-message
    >
      <div
        className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg border bg-primary text-primary-foreground shadow-xs"
      >
        <User className="size-4" />
      </div>
      <div className="min-w-0 max-w-[85%]">
        <div className="mb-1.5 flex min-h-9 items-center justify-end gap-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
          <div className="flex items-center gap-2">
            <span>You</span>
            {message.time && (
              <time
                dateTime={message.time}
                className="font-normal normal-case tracking-normal"
                title={new Date(message.time).toLocaleString()}
              >
                {formatMessageTime(message.time)}
              </time>
            )}
          {isDraft && !isFailed && (
            <span className="normal-case tracking-normal">Sending…</span>
          )}
          {isFailed && (
            <span className="normal-case tracking-normal text-destructive">
              Not sent
            </span>
          )}
          </div>
          {message.content && (
            <CopyButton content={message.content} label="task" />
          )}
        </div>
        <div className="rounded-2xl rounded-tr-md border bg-primary/10 px-4 py-3 text-sm leading-6 text-foreground">
          {message.content === "" ? (
            <LoadingDots />
          ) : (
            <ProcessedMessage
              messageContent={message.content}
              isUser={isUser}
              searchQuery={globalSearchQuery}
            />
          )}
        </div>
        {isFailed && draft && (
          <div
            className="mt-2 flex flex-wrap justify-end gap-1"
            role="alert"
            aria-label={`Message was not sent${draft.error ? `: ${draft.error}` : ""}`}
          >
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void onRetryMessage?.(draft.clientId)}
            >
              <RefreshCw />
              Retry
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => onEditMessage?.(draft.clientId, draft.content)}
            >
              <Pencil />
              Edit
            </Button>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={() => onDismissMessage?.(draft.clientId)}
              title="Dismiss failed message"
            >
              <X />
              <span className="sr-only">Dismiss failed message</span>
            </Button>
          </div>
        )}
      </div>
    </article>
  );
}
