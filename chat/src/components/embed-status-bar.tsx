"use client";

import { useMemo } from "react";
import { Hash } from "lucide-react";
import {useChat} from "./chat-provider";
import {AgentType} from "@/lib/agents";
import { computeTokenTotals, formatTokenCount, getStatusMeta } from "@/lib/session-status";

export function EmbedStatusBar() {
  const { agentState, loginRequired, agentType, agentapiVersion, richMessages, customTitle } = useChat();

  const status = getStatusMeta(agentState, loginRequired !== null);
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
            status.kind === "working" || status.kind === "reconnecting"
              ? "motion-safe:animate-spin"
              : ""
          }`}
        />
        <span className="truncate font-medium">{agentName}</span>
        {agentapiVersion && (
          <span className="shrink-0 text-muted-foreground">v{agentapiVersion}</span>
        )}
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
