import type { ComponentType } from "react";
import type { RichMessage, ServerStatus } from "@/components/chat-provider";
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

export function computeTokenTotals(richMessages: RichMessage[]): TokenTotals {
  const seen = new Set<string>();
  const totals: TokenTotals = { input: 0, output: 0, cacheRead: 0, cacheCreation: 0, total: 0 };
  for (const message of richMessages) {
    if (!message.usage || seen.has(message.message_id)) continue;
    seen.add(message.message_id);
    totals.input += message.usage.input_tokens;
    totals.output += message.usage.output_tokens;
    totals.cacheRead += message.usage.cache_read_input_tokens;
    totals.cacheCreation += message.usage.cache_creation_input_tokens;
  }
  totals.total = totals.input + totals.output;
  return totals;
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

// How the header and the embed bar show a state.
export function getStatusMeta(kind: AgentStateKind, loginRequired = false): StatusMeta {
  const meta = { kind, ...STATE_META[kind] };
  // A login comes before any answer: the agent can't run without it.
  if (kind === "needs-you" && loginRequired) meta.detail = "Agent is waiting for you to log in";
  return meta;
}
