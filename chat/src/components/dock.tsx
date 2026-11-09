"use client";

import {useChatSelector} from "./chat-provider";
import { sameValue } from "@/lib/agent-status";
import { formatTokens } from "@/lib/format";
import { useWorkingElapsed } from "@/lib/use-elapsed";
import { loginTitle } from "@/lib/login";

// The strip above the composer: what the agent is doing right now. It only
// shows while the agent works or waits for you; a ready agent needs no
// chrome. On a phone, waiting for you is left to the card that asks (it is
// still read out), as the screen has no room to say it twice.
export function StateStrip() {
  const kind = useChatSelector((chat) => chat.agentState);
  const serverStatus = useChatSelector((chat) => chat.serverStatus);
  const pendingRequestCount = useChatSelector((chat) => chat.pendingRequestCount);
  const loginRequired = useChatSelector((chat) => chat.loginRequired);
  const waitingFor = useChatSelector((chat) => chat.waitingFor);
  const queuedCount = useChatSelector((chat) => chat.queuedMessages.length);
  // A failed turn that waits to be tried again: no turn runs meanwhile.
  const retrying = useChatSelector((chat) => chat.retryWait !== null && chat.serverStatus === "stable");
  // What the agent does in its task and the tokens it took, worked out from
  // the task's latest steps (useTranscript): the strip renders again when
  // they change, not whenever a message streams.
  const activity = useChatSelector((chat) => chat.taskActivity);
  const tokens = useChatSelector((chat) => chat.taskTokens, sameValue);
  const elapsed = useWorkingElapsed(serverStatus);

  if (kind !== "working" && kind !== "needs-you") return null;
  const needs = kind === "needs-you";
  return (
    <div className={needs ? "grid gap-1.5 max-sm:sr-only" : "grid gap-1.5"} data-state-kind={kind}>
      {!needs && <div aria-hidden="true" className="state-scan h-px overflow-hidden rounded-full bg-border" />}
      <div className="flex min-w-0 items-center gap-3 px-1 text-xs tabular-nums text-muted-foreground" role="status">
        <span className={`flex shrink-0 items-center gap-1.5 font-semibold ${needs ? "text-state-needs" : "text-state-working"}`}>
          <span aria-hidden="true" className="state-led size-1.5 rounded-full bg-current" />
          {needs ? "Needs you" : "Working"}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-[11.5px]">
          {needs
            ? `${
                loginRequired
                  ? loginTitle(loginRequired)
                  : waitingFor === "limit"
                    ? "A usage limit stopped the agent"
                    : `The agent is waiting for your answer${pendingRequestCount > 1 ? ` · ${pendingRequestCount} requests` : ""}`
              }${queuedCount ? ` · ${queuedCount} queued ${queuedCount === 1 ? "task waits" : "tasks wait"}` : ""}`
            : retrying
              ? "Waiting to try the failed task again"
              : activity || "Working on the task"}
        </span>
        {!needs && elapsed && <span className="shrink-0">{elapsed}</span>}
        {!needs && !retrying && tokens.total > 0 && (
          <span className="hidden shrink-0 sm:inline" title="Tokens used by this task">
            ↑{formatTokens(tokens.input + tokens.cacheRead)} ↓{formatTokens(tokens.output)}
          </span>
        )}
      </div>
    </div>
  );
}
