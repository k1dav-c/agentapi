"use client";

import { useMemo } from "react";
import { Hash } from "lucide-react";
import { AgentType, useChat } from "./chat-provider";
import { computeTokenTotals, formatTokenCount, getStatusMeta } from "@/lib/session-status";

export function EmbedStatusBar() {
  const { serverStatus, connectionStatus, agentType, richMessages, customTitle } = useChat();

  const status = useMemo(
    () => getStatusMeta(serverStatus, connectionStatus),
    [serverStatus, connectionStatus],
  );
  const tokenTotals = useMemo(
    () => computeTokenTotals(richMessages),
    [richMessages],
  );
  const StatusIcon = status.icon;
  const agentName = customTitle
    ?? (agentType !== "unknown" && AgentType[agentType]
      ? AgentType[agentType].displayName
      : "Remote agent");

  return (
    <div className="flex h-8 shrink-0 items-center justify-between border-b bg-background/90 px-3 text-xs backdrop-blur-xl">
      <div className="flex min-w-0 items-center gap-2">
        <StatusIcon
          className={`size-3 shrink-0 ${status.className} ${
            serverStatus === "running" || connectionStatus === "reconnecting"
              ? "motion-safe:animate-spin"
              : ""
          }`}
        />
        <span className="truncate font-medium">{agentName}</span>
      </div>
      {tokenTotals.total > 0 && (
        <span className="flex shrink-0 items-center gap-1 tabular-nums text-muted-foreground">
          <Hash className="size-2.5" />
          {formatTokenCount(tokenTotals.total)}
        </span>
      )}
    </div>
  );
}
