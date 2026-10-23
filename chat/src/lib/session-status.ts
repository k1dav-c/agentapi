import type { ComponentType } from "react";
import {commonPrefix, type RichMessage, type Usage} from "@/lib/conversation";
import type {TaskSection} from "@/lib/task-timeline";
import {getToolCommand} from "@/lib/tool-format";
import {splitThinking} from "@/lib/thinking";
import type {ServerStatus} from "@/lib/agent-status";
import { CircleAlert, CircleCheck, CircleDot, LoaderCircle, WifiOff } from "lucide-react";

export interface TokenTotals {
  input: number;
  output: number;
  cacheRead: number;
  cacheCreation: number;
  total: number;
}

// A token count the way people read it: 950, 4.2k, 41k, 1.2M. The bounds
// are where rounding would reach the next unit (9,950 is 10k, not 10.0k).
export function formatTokenCount(count: number): string {
  if (count < 1000) return `${count}`;
  if (count < 9_950) return `${(count / 1000).toFixed(1)}k`;
  if (count < 999_500) return `${Math.round(count / 1000)}k`;
  return `${(count / 1_000_000).toFixed(1)}M`;
}

const noTokens: TokenTotals = {input: 0, output: 0, cacheRead: 0, cacheCreation: 0, total: 0};

function addUsage(totals: TokenTotals, usage: Usage): TokenTotals {
  const input = totals.input + usage.input_tokens;
  const output = totals.output + usage.output_tokens;
  return {
    input,
    output,
    cacheRead: totals.cacheRead + usage.cache_read_input_tokens,
    cacheCreation: totals.cacheCreation + usage.cache_creation_input_tokens,
    total: input + output,
  };
}

// The tokens the rich messages used; a message ID counts once (the agent
// may report the same model call's usage on several messages).
export function computeTokenTotals(richMessages: RichMessage[]): TokenTotals {
  const seen = new Set<string>();
  let totals = noTokens;
  for (const message of richMessages) {
    if (!message.usage || seen.has(message.message_id)) continue;
    seen.add(message.message_id);
    totals = addUsage(totals, message.usage);
  }
  return totals;
}

// TokenCounter adds up the tokens of the session's rich messages as they
// stream in, as computeTokenTotals does, going only through the messages
// after those that stayed the same objects. The totals keep their object
// while they don't change.
export class TokenCounter {
  private messages: RichMessage[] = [];
  // The totals after each message.
  private after: TokenTotals[] = [];
  // The IDs counted, and the place of the message each was counted at.
  private counted = new Map<string, number>();

  count(richMessages: RichMessage[]): TokenTotals {
    const unchanged = commonPrefix(this.messages, richMessages);
    for (let i = unchanged; i < this.messages.length; i++) {
      const id = this.messages[i].message_id;
      if (this.counted.get(id) === i) this.counted.delete(id);
    }
    const previous = this.after.at(-1) ?? noTokens;
    this.after.length = unchanged;
    let totals = this.after.at(-1) ?? noTokens;
    for (let i = unchanged; i < richMessages.length; i++) {
      const message = richMessages[i];
      if (message.usage && !this.counted.has(message.message_id)) {
        this.counted.set(message.message_id, i);
        totals = addUsage(totals, message.usage);
      }
      this.after.push(totals);
    }
    this.messages = richMessages;
    if (sameTotals(totals, previous)) {
      if (this.after.length > 0) this.after[this.after.length - 1] = previous;
      return previous;
    }
    return totals;
  }
}

function sameTotals(a: TokenTotals, b: TokenTotals): boolean {
  return a.input === b.input && a.output === b.output && a.cacheRead === b.cacheRead && a.cacheCreation === b.cacheCreation;
}

// What the agent does in a task, from its latest steps: the tool call it
// waits on, or what it thinks about ("" when neither); and the last tool it
// used. It reads from the task's end, so it costs little as the task grows.
export function taskProgress(task: TaskSection | undefined): {activity: string; latestTool: string | null} {
  let activity = "";
  let latestTool: string | null = null;
  const steps = task?.richActivity ?? [];
  for (let i = steps.length - 1; i >= 0 && (!activity || latestTool === null); i--) {
    const step = steps[i];
    if (step.type === "tool") {
      const call = step.toolCall;
      latestTool ??= call.name;
      if (!activity && call.resultTimestamp === undefined) {
        const command = getToolCommand(call.name, call.input);
        activity = command ? `$ ${command.split("\n")[0]}` : call.name;
      }
    } else if (step.type === "thinking" && !activity) {
      activity = `Thinking · ${splitThinking(step.content).title}`;
    }
  }
  return {activity, latestTool};
}

// What the agent is doing, from the user's point of view. "needs-you" wins
// over the activity status: an agent waiting on a dialog looks stable, but
// nothing (queued tasks included) moves until someone answers it.
export type AgentStateKind = "working" | "needs-you" | "ready" | "offline" | "reconnecting" | "connecting";

export interface StatusMeta {
  kind: AgentStateKind;
  label: string;
  detail: string;
  icon: ComponentType<{ className?: string }>;
  // Text color class for the state (see --state-* tokens in globals.css).
  className: string;
}

const STATE_META: Record<AgentStateKind, Omit<StatusMeta, "kind">> = {
  working: { label: "Working", detail: "Agent is working", icon: LoaderCircle, className: "text-state-working" },
  "needs-you": { label: "Needs you", detail: "Agent is waiting for your answer", icon: CircleDot, className: "text-state-needs" },
  ready: { label: "Ready", detail: "Agent is ready", icon: CircleCheck, className: "text-state-ready" },
  offline: { label: "Offline", detail: "Network connection lost", icon: WifiOff, className: "text-state-fault" },
  reconnecting: { label: "Reconnecting", detail: "Reconnecting to the agent server", icon: LoaderCircle, className: "text-muted-foreground" },
  connecting: { label: "Connecting", detail: "Waiting for agent status", icon: CircleAlert, className: "text-muted-foreground" },
};

export interface AgentStateInput {
  serverStatus: ServerStatus;
  connectionStatus: string;
  // Bottom of the terminal while it shows a prompt to answer (PTY).
  terminalPrompt?: string;
  // Requests the agent waits on (native transport: approvals, questions,
  // plans).
  pendingRequestCount?: number;
  // The agent waits for someone to log it in (native transport).
  loginRequired?: boolean;
}

// The agent needs you while its terminal shows a prompt, while it waits on
// requests or while it waits for someone to log it in.
export function getAgentState({
  serverStatus,
  connectionStatus,
  terminalPrompt = "",
  pendingRequestCount = 0,
  loginRequired = false,
}: AgentStateInput): AgentStateKind {
  if (connectionStatus === "offline" || serverStatus === "offline") return "offline";
  if (connectionStatus === "reconnecting") return "reconnecting";
  if (terminalPrompt || pendingRequestCount > 0 || loginRequired) return "needs-you";
  if (serverStatus === "running") return "working";
  if (serverStatus === "stable") return "ready";
  return "connecting";
}

// What the agent waits for while it needs you: a login comes before any
// answer, since the agent can't run without it. null when it doesn't wait.
export type WaitingFor = "login" | "answer" | null;

export function waitingFor(kind: AgentStateKind, loginRequired: boolean): WaitingFor {
  if (kind !== "needs-you") return null;
  return loginRequired ? "login" : "answer";
}

// How the header and the embed bar show a state.
export function getStatusMeta(kind: AgentStateKind, loginRequired = false): StatusMeta {
  const meta = { kind, ...STATE_META[kind] };
  // A login comes before any answer: the agent can't run without it.
  if (kind === "needs-you" && loginRequired) meta.detail = "Agent is waiting for you to log in";
  return meta;
}
