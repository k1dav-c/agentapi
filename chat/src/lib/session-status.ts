import type { ComponentType } from "react";
import type { RichMessage, ServerStatus } from "@/components/chat-provider";
import { CircleAlert, CircleCheck, LoaderCircle, WifiOff } from "lucide-react";

export interface TokenTotals {
  input: number;
  output: number;
  cacheRead: number;
  cacheCreation: number;
  total: number;
}

export function formatTokenCount(count: number): string {
  if (count < 1000) return `${count}`;
  if (count < 10_000) return `${(count / 1000).toFixed(1)}k`;
  if (count < 1_000_000) return `${Math.round(count / 1000)}k`;
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

export interface StatusMeta {
  label: string;
  detail: string;
  icon: ComponentType<{ className?: string }>;
  className: string;
}

export function getStatusMeta(
  serverStatus: ServerStatus,
  connectionStatus: string,
): StatusMeta {
  const agentStatus: Record<ServerStatus, StatusMeta> = {
    stable: {
      label: "Ready",
      detail: "Agent is ready",
      icon: CircleCheck,
      className: "text-status-success",
    },
    running: {
      label: "Working",
      detail: "Agent is processing",
      icon: LoaderCircle,
      className: "text-status-warning",
    },
    offline: {
      label: "Offline",
      detail: "Reconnecting to server",
      icon: WifiOff,
      className: "text-destructive",
    },
    unknown: {
      label: "Connecting",
      detail: "Waiting for agent status",
      icon: CircleAlert,
      className: "text-muted-foreground",
    },
  };

  if (connectionStatus === "offline") {
    return {
      label: "Offline",
      detail: "Network connection lost",
      icon: WifiOff,
      className: "text-destructive",
    };
  }

  if (connectionStatus === "reconnecting") {
    return {
      label: "Reconnecting",
      detail: "Reconnecting to agent server",
      icon: LoaderCircle,
      className: "text-status-warning",
    };
  }

  return agentStatus[serverStatus];
}
