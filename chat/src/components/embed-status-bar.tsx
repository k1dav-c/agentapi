"use client";

import { Hash } from "lucide-react";
import {useChatSelector} from "./chat-provider";
import {AgentType} from "@/lib/agents";
import {sameValue} from "@/lib/agent-status";
import { formatTokenCount, getStatusMeta } from "@/lib/session-status";

export function EmbedStatusBar() {
  const agentState = useChatSelector((chat) => chat.agentState);
  const loginRequired = useChatSelector((chat) => chat.loginRequired);
  const agentType = useChatSelector((chat) => chat.agentType);
  const agentapiVersion = useChatSelector((chat) => chat.agentapiVersion);
  const customTitle = useChatSelector((chat) => chat.customTitle);
  const tokenTotals = useChatSelector((chat) => chat.tokenTotals, sameValue);

  const status = getStatusMeta(agentState, loginRequired !== null);
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
