"use client";

import React, {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  CircleAlert,
  Check,
  CheckCircle2,
  Clipboard,
  Clock3,
  Code2,
  LoaderCircle,
  Sparkles,
  TerminalSquare,
  User,
  Wrench,
} from "lucide-react";
import { Button } from "./ui/button";
import type {
  AgentType,
  Message,
  RichMessage,
  ServerStatus,
} from "./chat-provider";
import {ProcessedMessage} from "./processed-message";
import {toast} from "sonner";

interface DraftMessage extends Omit<Message, "id"> {
  id?: number;
}

interface MessageListProps {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  serverStatus: ServerStatus;
  agentType: AgentType;
  onSelectPrompt?: (prompt: string) => void;
}

interface ToolCall {
  id: string;
  name: string;
  input?: unknown;
  result?: string;
  isError?: boolean;
  timestamp: string;
}

interface TaskSection {
  key: string;
  prompt: Message | DraftMessage;
  responses: (Message | DraftMessage)[];
  toolCalls: ToolCall[];
}

type TaskStatus = "queued" | "running" | "completed" | "failed";

function getTaskStatus(
  task: TaskSection,
  index: number,
  taskCount: number,
  serverStatus: ServerStatus,
): TaskStatus {
  if (task.prompt.id === undefined) return serverStatus === "running" ? "queued" : "running";
  if (task.toolCalls.some((tool) => tool.isError)) return "failed";
  if (index === taskCount - 1 && serverStatus === "running") return "running";
  return "completed";
}

export default function MessageList({
  messages,
  richMessages,
  serverStatus,
  agentType,
  onSelectPrompt,
}: MessageListProps) {
  const [scrollArea, setScrollArea] = useState<HTMLDivElement | null>(null);
  const [showScrollButton, setShowScrollButton] = useState(false);
  const [unreadCount, setUnreadCount] = useState(0);
  const [canScrollToPreviousUser, setCanScrollToPreviousUser] =
    useState(false);
  const [canScrollToNextUser, setCanScrollToNextUser] = useState(false);
  const isAtBottomRef = useRef(true);
  const lastScrollHeightRef = useRef(0);
  const userMessageCount = messages.filter(
    (message) => message.role === "user",
  ).length;
  const toolCalls = useMemo(() => collectToolCalls(richMessages), [richMessages]);
  const timeline = useMemo(() => {
    const tasks: TaskSection[] = [];
    const prelude: (Message | DraftMessage)[] = [];

    for (const message of messages) {
      if (message.role === "user") {
        tasks.push({
          key: `task-${message.id ?? `draft-${tasks.length}`}`,
          prompt: message,
          responses: [],
          toolCalls: [],
        });
      } else if (tasks.length > 0) {
        tasks.at(-1)!.responses.push(message);
      } else {
        prelude.push(message);
      }
    }

    for (const toolCall of toolCalls) {
      const toolTime = Date.parse(toolCall.timestamp);
      const target = [...tasks].reverse().find((task) => {
        if (!task.prompt.time) return false;
        return Date.parse(task.prompt.time) <= toolTime;
      });
      (target ?? tasks.at(-1))?.toolCalls.push(toolCall);
    }

    return {prelude, tasks};
  }, [messages, toolCalls]);
  const contentSignature = useMemo(
    () =>
      [
        ...timeline.prelude.map((message, index) =>
          `prelude-${message.id ?? index}:${message.content.length}`),
        ...timeline.tasks.map((task) =>
          `${task.key}:${task.prompt.content.length}:${task.responses
            .map((message) => message.content.length)
            .join(",")}:${task.toolCalls
            .map((tool) => `${tool.id}:${tool.result?.length ?? -1}`)
            .join(",")}`),
      ].join("|"),
    [timeline],
  );

  const scrollToBottom = useCallback(
    (behavior: ScrollBehavior = "smooth") => {
      scrollArea?.scrollTo({ top: scrollArea.scrollHeight, behavior });
      isAtBottomRef.current = true;
      setShowScrollButton(false);
      setUnreadCount(0);
    },
    [scrollArea],
  );

  const scrollToPreviousUserMessage = useCallback(() => {
    if (!scrollArea) return;

    const targetTop = getPreviousUserMessageTop(scrollArea);
    if (targetTop === undefined) return;
    scrollArea.scrollTo({
      top: Math.max(0, targetTop - 16),
      behavior: "smooth",
    });
  }, [scrollArea]);

  const scrollToNextUserMessage = useCallback(() => {
    if (!scrollArea) return;
    const targetTop = getNextUserMessageTop(scrollArea);
    if (targetTop === undefined) return;
    scrollArea.scrollTo({top: Math.max(0, targetTop - 16), behavior: "smooth"});
  }, [scrollArea]);

  useEffect(() => {
    if (!scrollArea) return;

    const handleScroll = () => {
      const { scrollTop, scrollHeight, clientHeight } = scrollArea;
      const atBottom = scrollTop + clientHeight >= scrollHeight - 32;
      isAtBottomRef.current = atBottom;
      setShowScrollButton(!atBottom);
      if (atBottom) setUnreadCount(0);
      setCanScrollToPreviousUser(
        getPreviousUserMessageTop(scrollArea) !== undefined,
      );
      setCanScrollToNextUser(getNextUserMessageTop(scrollArea) !== undefined);
    };

    handleScroll();
    scrollArea.addEventListener("scroll", handleScroll, { passive: true });
    return () => scrollArea.removeEventListener("scroll", handleScroll);
  }, [scrollArea, userMessageCount]);

  useLayoutEffect(() => {
    if (!scrollArea) return;

    const currentHeight = scrollArea.scrollHeight;
    const hasNewContent = currentHeight > lastScrollHeightRef.current;
    const isFirstRender = lastScrollHeightRef.current === 0;
    const isNewUserMessage = messages.at(-1)?.role === "user";

    if (
      hasNewContent &&
      (isFirstRender || isAtBottomRef.current || isNewUserMessage)
    ) {
      scrollToBottom(isFirstRender || serverStatus === "running" ? "auto" : "smooth");
    } else if (hasNewContent) {
      setUnreadCount((count) => count + 1);
    }
    lastScrollHeightRef.current = currentHeight;
  }, [contentSignature, messages, scrollArea, scrollToBottom, serverStatus]);

  return (
    <div className="relative min-h-0 flex-1">
      <div
        className="h-full overflow-y-auto overscroll-contain"
        ref={setScrollArea}
      >
        {timeline.prelude.length === 0 && timeline.tasks.length === 0 ? (
          <EmptyState
            serverStatus={serverStatus}
            agentType={agentType}
            onSelectPrompt={onSelectPrompt}
          />
        ) : (
          <div className="mx-auto flex w-full max-w-5xl flex-col gap-7 px-3 py-6 sm:px-6 sm:py-10">
            {timeline.prelude.map((message, index) => (
              <MessageItem key={`prelude-${message.id ?? index}`} message={message} />
            ))}
            {timeline.tasks.map((task, index) => (
              <TaskGroup
                key={task.key}
                task={task}
                number={index + 1}
                status={getTaskStatus(task, index, timeline.tasks.length, serverStatus)}
              />
            ))}
          </div>
        )}
      </div>

      {(canScrollToPreviousUser || canScrollToNextUser) && (
        <div className="absolute bottom-4 right-3 z-10 flex overflow-hidden rounded-full border bg-background/95 shadow-lg backdrop-blur sm:right-4">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            disabled={!canScrollToPreviousUser}
            onClick={scrollToPreviousUserMessage}
            className="size-11 rounded-none"
            title="Previous task"
          >
            <ArrowUp />
            <span className="sr-only">Jump to previous task</span>
          </Button>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            disabled={!canScrollToNextUser}
            onClick={scrollToNextUserMessage}
            className="size-11 rounded-none border-l"
            title="Next task"
          >
            <ArrowDown />
            <span className="sr-only">Jump to next task</span>
          </Button>
        </div>
      )}

      {showScrollButton && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => scrollToBottom()}
          className="absolute bottom-4 left-3 z-10 h-11 gap-2 rounded-full bg-background/95 px-3 shadow-lg backdrop-blur sm:left-1/2 sm:-translate-x-1/2"
          title="Jump to latest message"
        >
          <ArrowDown className="size-3.5" />
          <span>
            {unreadCount > 0
              ? `${unreadCount} new ${unreadCount === 1 ? "update" : "updates"}`
              : "Jump to latest"}
          </span>
        </Button>
      )}
    </div>
  );
}

function getPreviousUserMessageTop(scrollArea: HTMLDivElement) {
  const scrollAreaTop = scrollArea.getBoundingClientRect().top;
  const userMessages = Array.from(
    scrollArea.querySelectorAll<HTMLElement>("[data-user-message]"),
  );

  return userMessages
    .map(
      (message) =>
        message.getBoundingClientRect().top -
        scrollAreaTop +
        scrollArea.scrollTop,
    )
    .findLast((position) => position < scrollArea.scrollTop - 8);
}

function getNextUserMessageTop(scrollArea: HTMLDivElement) {
  const scrollAreaTop = scrollArea.getBoundingClientRect().top;
  const positions = Array.from(
    scrollArea.querySelectorAll<HTMLElement>("[data-user-message]"),
  ).map(
    (message) =>
      message.getBoundingClientRect().top -
      scrollAreaTop +
      scrollArea.scrollTop,
  );
  return positions.find((position) => position > scrollArea.scrollTop + 24);
}

function collectToolCalls(richMessages: RichMessage[]): ToolCall[] {
  const calls = new Map<string, ToolCall>();

  for (const message of richMessages) {
    for (const block of message.content) {
      if (block.type === "tool_use" && block.tool_use_id) {
        calls.set(block.tool_use_id, {
          ...calls.get(block.tool_use_id),
          id: block.tool_use_id,
          name: block.tool_name || "Tool",
          input: block.tool_input,
          timestamp: message.timestamp,
        });
      }

      if (block.type === "tool_result" && block.tool_use_id) {
        const existing = calls.get(block.tool_use_id);
        calls.set(block.tool_use_id, {
          id: block.tool_use_id,
          name: existing?.name || "Tool",
          input: existing?.input,
          result: block.text ?? "",
          isError: block.is_error,
          timestamp: existing?.timestamp || message.timestamp,
        });
      }
    }
  }

  return [...calls.values()];
}

function formatToolInput(input: unknown): string {
  if (input === undefined || input === null) return "";
  if (typeof input === "string") {
    try {
      return JSON.stringify(JSON.parse(input), null, 2);
    } catch {
      return input;
    }
  }

  try {
    return JSON.stringify(input, null, 2);
  } catch {
    return String(input);
  }
}

function ToolCallCard({ toolCall }: { toolCall: ToolCall }) {
  const isPending = toolCall.result === undefined;
  const input = formatToolInput(toolCall.input);

  return (
    <details className="group overflow-hidden rounded-xl border border-l-2 bg-card/70 shadow-xs">
      <summary className="flex cursor-pointer list-none items-center gap-3 px-4 py-3 transition hover:bg-muted/45 [&::-webkit-details-marker]:hidden">
        <span className="grid size-8 shrink-0 place-items-center rounded-lg border bg-background">
          <Wrench className="size-4" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">
            {toolCall.name}
          </span>
          <span className="block text-xs text-muted-foreground">
            {toolCall.isError
              ? "Tool call failed"
              : isPending
                ? "Tool call is running"
                : "Tool call completed"}
          </span>
        </span>
        {toolCall.isError ? (
          <CircleAlert className="size-4 shrink-0 text-destructive" />
        ) : isPending ? (
          <LoaderCircle className="size-4 shrink-0 animate-spin text-muted-foreground" />
        ) : (
          <CheckCircle2 className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
        )}
        <ArrowDown className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
      </summary>
      <div className="space-y-4 border-t bg-muted/20 px-4 py-4">
        {input && <ToolDetail label="Input" content={input} />}
        {toolCall.result !== undefined && (
          <ToolDetail
            label={toolCall.isError ? "Error" : "Result"}
            content={toolCall.result || "(No output)"}
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

function ToolActivityGroup({toolCalls}: {toolCalls: ToolCall[]}) {
  const pending = toolCalls.filter((tool) => tool.result === undefined).length;
  const failed = toolCalls.filter((tool) => tool.isError).length;

  return (
    <details className="group ml-3 overflow-hidden rounded-xl border bg-card/70 shadow-xs sm:ml-8">
      <summary className="flex min-h-12 cursor-pointer list-none items-center gap-3 px-4 py-3 transition hover:bg-muted/45 [&::-webkit-details-marker]:hidden">
        <span className="grid size-8 shrink-0 place-items-center rounded-lg border bg-background">
          <Wrench className="size-4" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block text-sm font-medium">
            Tool activity · {toolCalls.length}
          </span>
          <span className="block text-xs text-muted-foreground">
            {failed > 0
              ? `${failed} failed`
              : pending > 0
                ? `${pending} running`
                : "All tool calls completed"}
          </span>
        </span>
        <ChevronDown className="size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
      </summary>
      <div className="space-y-2 border-t bg-muted/15 p-2 sm:p-3">
        {toolCalls.map((toolCall) => (
          <ToolCallCard key={toolCall.id} toolCall={toolCall} />
        ))}
      </div>
    </details>
  );
}

function ToolDetail({ label, content }: { label: string; content: string }) {
  return (
    <section>
      <h3 className="mb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </h3>
      <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border bg-background p-3 font-mono text-xs leading-5">
        {content}
      </pre>
    </section>
  );
}

function TaskGroup({
  task,
  number,
  status,
}: {
  task: TaskSection;
  number: number;
  status: TaskStatus;
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
      className: "text-amber-600 dark:text-amber-400",
    },
    completed: {
      label: "Completed",
      icon: CheckCircle2,
      className: "text-emerald-600 dark:text-emerald-400",
    },
    failed: {
      label: "Failed",
      icon: CircleAlert,
      className: "text-destructive",
    },
  }[status];
  const StatusIcon = statusMeta.icon;

  return (
    <section className="overflow-hidden rounded-2xl border bg-background/70 shadow-sm">
      <header className="flex min-h-11 items-center justify-between gap-3 border-b bg-muted/25 px-4 py-2">
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Task {number}
        </span>
        <span
          className={`flex items-center gap-1.5 text-xs font-medium ${statusMeta.className}`}
          role="status"
        >
          <StatusIcon
            className={`size-3.5 ${status === "running" ? "motion-safe:animate-spin" : ""}`}
          />
          {statusMeta.label}
        </span>
      </header>
      <div className="space-y-6 p-4 sm:p-5">
        <MessageItem message={task.prompt} />
        {task.toolCalls.length > 0 && (
          <ToolActivityGroup toolCalls={task.toolCalls} />
        )}
        {task.responses.map((message, index) => (
          <MessageItem
            key={`response-${message.id ?? index}`}
            message={message}
          />
        ))}
      </div>
    </section>
  );
}

function EmptyState({
  serverStatus,
  agentType,
  onSelectPrompt,
}: {
  serverStatus: ServerStatus;
  agentType: AgentType;
  onSelectPrompt?: (prompt: string) => void;
}) {
  const isOffline = serverStatus === "offline";
  const name =
    agentType === "unknown" ? "your coding agent" : agentType.replace("-", " ");

  return (
    <div className="mx-auto flex h-full w-full max-w-3xl flex-col items-center justify-center px-6 py-12 text-center">
      <div className="relative mb-7">
        <div className="absolute inset-0 scale-150 rounded-full bg-primary/10 blur-2xl" />
        <div className="relative grid size-16 place-items-center rounded-2xl border bg-card shadow-sm">
          <TerminalSquare className="size-7" />
        </div>
      </div>
      <p className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">
        <Sparkles className="size-3.5" />
        Live agent workspace
      </p>
      <h1 className="max-w-xl text-balance text-2xl font-semibold tracking-tight sm:text-3xl">
        {isOffline ? "The agent server is offline" : `Start working with ${name}`}
      </h1>
      <p className="mt-3 max-w-lg text-pretty text-sm leading-6 text-muted-foreground">
        {isOffline
          ? "AgentAPI is trying to reconnect. Check the server URL and make sure the agent process is running."
          : "Send a task, attach project files, or switch to Control mode when the terminal needs direct input."}
      </p>
      {!isOffline && (
        <div className="mt-8 grid w-full max-w-lg grid-cols-1 gap-3 text-left sm:grid-cols-2">
          <PromptHint
            icon={Code2}
            text="Review the current codebase"
            prompt="Review the current codebase and suggest the highest-impact improvements."
            onSelect={onSelectPrompt}
          />
          <PromptHint
            icon={TerminalSquare}
            text="Investigate a failing test"
            prompt="Run the test suite, investigate any failures, and explain the root cause."
            onSelect={onSelectPrompt}
          />
        </div>
      )}
    </div>
  );
}

function PromptHint({
  icon: Icon,
  text,
  prompt,
  onSelect,
}: {
  icon: React.ComponentType<{ className?: string }>;
  text: string;
  prompt: string;
  onSelect?: (prompt: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onSelect?.(prompt)}
      className="flex min-h-12 items-center gap-3 rounded-xl border bg-card/60 p-3 text-left text-xs text-muted-foreground shadow-xs transition hover:bg-card hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2"
    >
      <Icon className="size-4 shrink-0 text-foreground" />
      <span>{text}</span>
    </button>
  );
}

function MessageItem({
  message,
}: {
  message: Message | DraftMessage;
}) {
  const isUser = message.role === "user";
  const isDraft = message.id === undefined;

  if (!isUser) {
    return (
      <article className="min-w-0">
        <div className="mb-2 flex h-7 items-center justify-between gap-3">
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
          {message.content && <CopyButton content={message.content} />}
        </div>
        {message.content === "" ? (
          <LoadingDots />
        ) : (
          <ProcessedMessage
            messageContent={message.content}
            isUser={false}
          />
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
        className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg border bg-foreground text-background shadow-xs"
      >
        <User className="size-4" />
      </div>
      <div className="min-w-0 max-w-[85%]">
        <div className="mb-1.5 flex items-center justify-end gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
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
          {isDraft && <span className="normal-case tracking-normal">Sending…</span>}
        </div>
        <div className="rounded-2xl rounded-tr-md bg-foreground px-4 py-3 text-sm leading-6 text-background shadow-sm">
          {message.content === "" ? (
            <LoadingDots />
          ) : (
            <ProcessedMessage
              messageContent={message.content}
              isUser={isUser}
            />
          )}
        </div>
      </div>
    </article>
  );
}

function CopyButton({ content }: { content: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard access can be blocked in embedded or non-secure contexts.
      setCopied(false);
      toast.error("Could not copy the response", {
        description: "Clipboard access may be blocked in this browser context.",
      });
    }
  };

  return (
    <button
      type="button"
      onClick={copy}
      className="grid size-11 shrink-0 place-items-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground sm:size-9"
      title="Copy response"
    >
      {copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}
      <span className="sr-only">Copy response</span>
    </button>
  );
}

const LoadingDots = () => (
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

function formatMessageTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
}
