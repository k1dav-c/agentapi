"use client";

import React, { useState } from "react";
import { ChevronRight, CornerDownLeft } from "lucide-react";
import { AgentType, type DraftMessage, type Message, type ServerStatus } from "../chat-provider";
import { MessageItem } from "./message-item";

const STARTERS = [
  { text: "Explain this project", prompt: "Give me an overview of the project structure and architecture." },
  { text: "Investigate a failing test", prompt: "Run the test suite, investigate any failures, and explain the root cause." },
  { text: "Review the current changes", prompt: "Review the uncommitted changes and point out bugs or risky parts." },
  { text: "Set up the dev environment", prompt: "Check the project setup, install dependencies, and make sure the dev environment is ready." },
];

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
  onSelectPrompt,
  startup = [],
}: {
  serverStatus: ServerStatus;
  agentType: AgentType;
  onSelectPrompt?: (prompt: string) => void;
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
      <h1 className="mt-2 text-balance text-3xl font-semibold tracking-[-0.02em] sm:text-4xl">{heading}</h1>
      <p className="mt-3 max-w-[34rem] text-pretty text-[15px] leading-[1.65] text-muted-foreground">{detail}</p>
      {serverStatus !== "offline" && (
        <ul className="mt-8 border-t">
          {STARTERS.map((starter) => (
            <li key={starter.text} className="border-b">
              <button
                type="button"
                onClick={() => onSelectPrompt?.(starter.prompt)}
                className="group flex w-full items-center gap-3 py-3 text-left text-[15px] outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              >
                <span className="flex-1">{starter.text}</span>
                <CornerDownLeft className="size-3.5 text-muted-foreground opacity-0 transition group-hover:opacity-100 group-focus-visible:opacity-100" />
              </button>
            </li>
          ))}
        </ul>
      )}
      {startup.length > 0 && (
        <div className="mt-6">
          <StartupScreen messages={startup} inset={false} />
        </div>
      )}
    </div>
  );
}
