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
  CircleAlert,
  Check,
  CheckCircle2,
  Clipboard,
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

interface DraftMessage extends Omit<Message, "id"> {
  id?: number;
}

interface MessageListProps {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  serverStatus: ServerStatus;
  agentType: AgentType;
}

interface ToolCall {
  id: string;
  name: string;
  input?: unknown;
  result?: string;
  isError?: boolean;
  timestamp: string;
}

export default function MessageList({
  messages,
  richMessages,
  serverStatus,
  agentType,
}: MessageListProps) {
  const [scrollArea, setScrollArea] = useState<HTMLDivElement | null>(null);
  const [showScrollButton, setShowScrollButton] = useState(false);
  const isAtBottomRef = useRef(true);
  const lastScrollHeightRef = useRef(0);
  const toolCalls = useMemo(() => collectToolCalls(richMessages), [richMessages]);
  const timeline = useMemo(() => {
    const entries = [
      ...messages.map((message, index) => ({
        type: "message" as const,
        key: `message-${message.id ?? `draft-${index}`}`,
        timestamp: message.time,
        message,
        index,
      })),
      ...toolCalls.map((toolCall, index) => ({
        type: "tool" as const,
        key: `tool-${toolCall.id || index}`,
        timestamp: toolCall.timestamp,
        toolCall,
      })),
    ];

    return entries.sort((left, right) => {
      if (!left.timestamp && !right.timestamp) return 0;
      if (!left.timestamp) return 1;
      if (!right.timestamp) return -1;
      return Date.parse(left.timestamp) - Date.parse(right.timestamp);
    });
  }, [messages, toolCalls]);

  const scrollToBottom = useCallback(
    (behavior: ScrollBehavior = "smooth") => {
      scrollArea?.scrollTo({ top: scrollArea.scrollHeight, behavior });
      isAtBottomRef.current = true;
      setShowScrollButton(false);
    },
    [scrollArea],
  );

  useEffect(() => {
    if (!scrollArea) return;

    const handleScroll = () => {
      const { scrollTop, scrollHeight, clientHeight } = scrollArea;
      const atBottom = scrollTop + clientHeight >= scrollHeight - 32;
      isAtBottomRef.current = atBottom;
      setShowScrollButton(!atBottom);
    };

    handleScroll();
    scrollArea.addEventListener("scroll", handleScroll, { passive: true });
    return () => scrollArea.removeEventListener("scroll", handleScroll);
  }, [scrollArea]);

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
      scrollToBottom(isFirstRender ? "auto" : "smooth");
    }
    lastScrollHeightRef.current = currentHeight;
  }, [messages, scrollArea, scrollToBottom]);

  return (
    <div className="relative min-h-0 flex-1">
      <div
        className="h-full overflow-y-auto overscroll-contain scroll-smooth"
        ref={setScrollArea}
      >
        {timeline.length === 0 ? (
          <EmptyState serverStatus={serverStatus} agentType={agentType} />
        ) : (
          <div className="mx-auto flex w-full max-w-6xl flex-col gap-7 px-4 py-8 sm:px-6 sm:py-10">
            {timeline.map((entry) =>
              entry.type === "message" ? (
                <MessageItem
                  key={entry.key}
                  message={entry.message}
                  index={entry.index}
                />
              ) : (
                <ToolCallCard key={entry.key} toolCall={entry.toolCall} />
              ),
            )}
          </div>
        )}
      </div>

      {showScrollButton && (
        <Button
          type="button"
          size="icon"
          variant="outline"
          onClick={() => scrollToBottom()}
          className="absolute bottom-4 left-1/2 z-10 -translate-x-1/2 rounded-full bg-background/90 shadow-lg backdrop-blur"
          title="Jump to latest message"
        >
          <ArrowDown />
          <span className="sr-only">Jump to latest message</span>
        </Button>
      )}
    </div>
  );
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
    <details className="group overflow-hidden rounded-xl border bg-card/70 shadow-xs">
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

function EmptyState({
  serverStatus,
  agentType,
}: {
  serverStatus: ServerStatus;
  agentType: AgentType;
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
          <Hint icon={Code2} text="Ask the agent to inspect or change code" />
          <Hint icon={TerminalSquare} text="Send terminal keys in Control mode" />
        </div>
      )}
    </div>
  );
}

function Hint({
  icon: Icon,
  text,
}: {
  icon: React.ComponentType<{ className?: string }>;
  text: string;
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl border bg-card/60 p-3 text-xs text-muted-foreground shadow-xs">
      <Icon className="size-4 shrink-0 text-foreground" />
      <span>{text}</span>
    </div>
  );
}

function MessageItem({
  message,
  index,
}: {
  message: Message | DraftMessage;
  index: number;
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
            index={index}
            isUser={false}
          />
        )}
      </article>
    );
  }

  return (
    <article className="flex flex-row-reverse gap-3 sm:gap-4">
      <div
        className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg border bg-foreground text-background shadow-xs"
      >
        <User className="size-4" />
      </div>
      <div className="min-w-0 max-w-[85%]">
        <div className="mb-1.5 flex items-center justify-end gap-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
          <span>You</span>
          {isDraft && <span className="normal-case tracking-normal">Sending…</span>}
        </div>
        <div className="rounded-2xl rounded-tr-md bg-foreground px-4 py-3 text-sm leading-6 text-background shadow-sm">
          {message.content === "" ? (
            <LoadingDots />
          ) : (
            <ProcessedMessage
              messageContent={message.content}
              index={index}
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
    await navigator.clipboard.writeText(content);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <button
      type="button"
      onClick={copy}
      className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground transition hover:bg-muted hover:text-foreground"
      title="Copy response"
    >
      {copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}
      <span className="sr-only">Copy response</span>
    </button>
  );
}

const LoadingDots = () => (
  <div className="flex h-6 items-center gap-1.5" aria-label="Agent is responding">
    {[0, 150, 300].map((delay) => (
      <span
        key={delay}
        className="size-1.5 animate-pulse rounded-full bg-muted-foreground"
        style={{ animationDelay: `${delay}ms` }}
      />
    ))}
  </div>
);

const ProcessedMessage = React.memo(function ProcessedMessage({
  messageContent,
  index,
  isUser,
}: {
  messageContent: string;
  index: number;
  isUser: boolean;
}) {
  const urlRegex = useMemo(
    () => /(https?:\/\/[^\s<]+|www\.[^\s<]+)/g,
    [],
  );

  const linkedContent = useMemo(
    () =>
      messageContent.split(urlRegex).map((content, partIndex) => {
        const isUrl = /^(https?:\/\/|www\.)/.test(content);
        if (!isUrl) return <span key={`${index}-${partIndex}`}>{content}</span>;

        const href = content.startsWith("www.") ? `https://${content}` : content;
        return (
          <a
            key={`${index}-${partIndex}`}
            href={href}
            target="_blank"
            rel="noreferrer"
            className="underline decoration-current/30 underline-offset-4 transition hover:decoration-current"
          >
            {content}
          </a>
        );
      }),
    [index, messageContent, urlRegex],
  );

  return (
    <div
      className={`text-left ${
        isUser
          ? "whitespace-pre-wrap break-words text-sm leading-6"
          : "overflow-x-auto whitespace-pre font-mono text-[13px] leading-5 [tab-size:4]"
      }`}
    >
      {linkedContent}
    </div>
  );
});
