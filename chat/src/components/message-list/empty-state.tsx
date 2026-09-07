"use client";

import React from "react";
import { Code2, Search, Sparkles, TerminalSquare, Wrench } from "lucide-react";
import { AgentType, type ServerStatus } from "../chat-provider";

export function PromptHint({
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

export function EmptyState({
  serverStatus,
  agentType,
  onSelectPrompt,
}: {
  serverStatus: ServerStatus;
  agentType: AgentType;
  onSelectPrompt?: (prompt: string) => void;
}) {
  const isOffline = serverStatus === "offline";
  const name = agentType !== "unknown" && AgentType[agentType]
    ? AgentType[agentType].displayName
    : "your coding agent";

  const defaultPrompts = [
    { icon: Code2, text: "Review the current codebase", prompt: "Review the current codebase and suggest the highest-impact improvements." },
    { icon: TerminalSquare, text: "Investigate a failing test", prompt: "Run the test suite, investigate any failures, and explain the root cause." },
    { icon: Search, text: "Explain this project", prompt: "Give me an overview of the project structure and architecture." },
    { icon: Wrench, text: "Set up the dev environment", prompt: "Check the project setup, install dependencies, and make sure the dev environment is ready." },
  ];

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
            : "Send a task, attach project files, or switch to Terminal input when the agent needs direct keystrokes."}
      </p>
      {!isOffline && (
        <div className="mt-8 grid w-full max-w-lg grid-cols-1 gap-3 text-left sm:grid-cols-2">
          {defaultPrompts.map((hint) => (
            <PromptHint
              key={hint.text}
              icon={hint.icon}
              text={hint.text}
              prompt={hint.prompt}
              onSelect={onSelectPrompt}
            />
          ))}
        </div>
      )}
    </div>
  );
}
