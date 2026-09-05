"use client";

import React, { useMemo, useState } from "react";
import { Check, Clipboard, Code2, FileText, Keyboard, Pencil, RefreshCw, Search, TerminalSquare, User, X } from "lucide-react";
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
}: {
  message: Message | DraftMessage;
  onRetryMessage?: (clientId: string) => Promise<boolean>;
  onEditMessage?: (clientId: string, content: string) => void;
  onDismissMessage?: (clientId: string) => void;
  searchQuery?: string;
}) {
  const isUser = message.role === "user";
  const isDraft = message.id === undefined;
  const draft = isDraft ? (message as DraftMessage) : undefined;
  const isFailed = draft?.deliveryStatus === "failed";
  const [searchOpen, setSearchOpen] = useState(false);
  const [outputSearchQuery, setOutputSearchQuery] = useState("");
  const [renderMode, setRenderMode] = useState<"raw" | "markdown">("raw");
  const effectiveSearchQuery = outputSearchQuery || globalSearchQuery;

  // Detect when the agent's terminal output contains an interactive TUI
  // prompt that requires the user to switch to Terminal mode to respond.
  // Covers Claude Code (Ink UI) and Codex (TUI) interactive elements:
  // selection menus, confirmation dialogs, permission prompts, login
  // flows, plan approval, AskUserQuestion, MCP elicitation, etc.
  const terminalActionNeeded = useMemo(() => {
    if (isUser || !message.content) return null;
    const c = message.content;

    // --- Selection / choice UI ---
    // Claude uses ❯, Codex uses > or ›  as cursor indicator
    const hasCursorSelection = /[❯›]\s*\d+\./m.test(c) || /[❯›]\s*(Yes|No|Skip)/m.test(c);
    // Codex: "> N." at line start (but NOT shell output "$ >" or quote ">")
    const hasCodexSelection = /^\s*>\s*\d+\.\s/m.test(c) && /Press Enter/i.test(c);

    // --- Confirmation / action prompts ---
    const hasConfirmPrompt =
      c.includes("Enter to confirm") ||
      c.includes("Esc to cancel") ||
      /Press Enter to (continue|connect|install|retry|open)/i.test(c);

    // --- Permission / approval ---
    // "Do you want to proceed?" / "Allow" + numbered options
    const hasPermissionUI =
      (c.includes("Do you want to") && /[❯›>]\s*\d/m.test(c)) ||
      (c.includes("Allow") && c.includes("Deny"));

    // --- Auth / login flows ---
    // Match authorize/authentication/Login/sign in, but NOT when they
    // appear as part of a file path or code (require surrounding context)
    const hasAuthUI =
      /authori[zs][ae]/i.test(c) ||
      /\bsign in\b/i.test(c) ||
      c.includes("needs your input") ||
      c.includes("needs your approval") ||
      c.includes("run /login") ||
      c.includes("run /mcp");

    // --- MCP elicitation ---
    const hasMcpUI =
      c.includes("MCP server needs your input") ||
      c.includes("MCP server needs your") ||
      c.includes("Do you want to allow this connection");

    // --- Plan mode ---
    const hasPlanUI =
      (c.includes("Would you like to proceed") && /[❯›>]\s*\d/m.test(c)) ||
      (c.includes("Ready to code") && /[❯›>]\s*\d/m.test(c));

    // --- Codex-specific ---
    const hasCodexApproval =
      (/wants to edit\b/i.test(c) && !c.includes("Do you want")) ||
      (/wants to run\b/i.test(c) && !c.includes("Do you want")) ||
      (c.includes("approve") && /network access/i.test(c));

    if (
      hasCursorSelection || hasCodexSelection || hasConfirmPrompt ||
      hasPermissionUI || hasAuthUI || hasMcpUI || hasPlanUI || hasCodexApproval
    ) {
      return "This prompt requires terminal input. Switch to the Terminal tab below to respond.";
    }
    return null;
  }, [isUser, message.content]);
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
                    onClick={() => setRenderMode((mode) => mode === "raw" ? "markdown" : "raw")}
                    className={`grid size-9 place-items-center rounded-md text-muted-foreground outline-none transition hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring ${renderMode === "markdown" ? "bg-muted text-foreground" : ""}`}
                    aria-label={renderMode === "markdown" ? "Show this block as raw text" : "Preview this block as Markdown"}
                    aria-pressed={renderMode === "markdown"}
                  >
                    {renderMode === "markdown" ? <Code2 className="size-3.5" /> : <FileText className="size-3.5" />}
                  </button>
                </TooltipTrigger>
                <TooltipContent>{renderMode === "markdown" ? "Show raw" : "Preview Markdown"}</TooltipContent>
              </Tooltip>
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
        {terminalActionNeeded && (
          <div
            className="mb-2 flex items-center gap-2 rounded-lg border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning"
            role="alert"
          >
            <Keyboard className="size-3.5 shrink-0" />
            <span>{terminalActionNeeded}</span>
          </div>
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
