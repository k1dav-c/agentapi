"use client";

import React, { useState } from "react";
import { ChevronRight } from "lucide-react";
import { AgentType, type DraftMessage, type Message, type ServerStatus } from "../chat-provider";
import { MessageItem } from "./message-item";

// Terminal output from before the first task. Usually that is just the
// agent's startup banner, which stays collapsed instead of reading as
// content; if you worked in TTY mode (prompts typed straight into the
// terminal never become tasks), it holds the real conversation and opens.
export function StartupScreen({
  messages,
  inset = true,
}: {
  messages: (Message | DraftMessage)[];
  // Align with the task column (past the time rail).
  inset?: boolean;
}) {
  // Agent replies start with ● (Claude Code) or • (Codex); a startup
  // banner has none.
  const substantial = messages.some((message) => /^\s*[●•⏺]\s+\S/m.test(message.content));
  const [open, setOpen] = useState(substantial);
  return (
    <div className={`py-3 ${inset ? "sm:pl-[4.75rem]" : ""}`}>
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        className="flex items-center gap-1.5 rounded text-xs text-muted-foreground outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ChevronRight className={`size-3 transition-transform ${open ? "rotate-90" : ""}`} />
        Terminal output
      </button>
      {open && (
        <div className="mt-2 space-y-2">
          {messages.map((message, index) => (
            <MessageItem key={`startup-${message.id ?? index}`} message={message} />
          ))}
        </div>
      )}
    </div>
  );
}

export function EmptyState({
  serverStatus,
  agentType,
  startup = [],
}: {
  serverStatus: ServerStatus;
  agentType: AgentType;
  startup?: (Message | DraftMessage)[];
}) {
  const name = agentType !== "unknown" && AgentType[agentType]
    ? AgentType[agentType].displayName
    : "The agent";
  const heading =
    serverStatus === "offline" ? "The agent server is offline"
    : serverStatus === "stable" ? `${name} is ready`
    : serverStatus === "running" ? `${name} is starting`
    : "Connecting to the agent";
  const detail =
    serverStatus === "offline"
      ? "AgentAPI keeps trying to reconnect. Check that the server and the agent process are running."
      : "Describe a task below. Tasks you send while the agent is busy wait in the queue and run in order.";

  return (
    <div className="mx-auto flex min-h-full w-full max-w-[44rem] flex-col justify-center px-6 py-12">
      <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">New session</p>
      <h2 className="mt-2 text-balance text-3xl font-semibold tracking-[-0.02em] sm:text-4xl">{heading}</h2>
      <p className="mt-3 max-w-[34rem] text-pretty text-[15px] leading-[1.65] text-muted-foreground">{detail}</p>
      {startup.length > 0 && (
        <div className="mt-6">
          <StartupScreen messages={startup} inset={false} />
        </div>
      )}
    </div>
  );
}
