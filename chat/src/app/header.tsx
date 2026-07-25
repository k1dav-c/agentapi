"use client";

import { AgentType, useChat } from "@/components/chat-provider";
import { ModeToggle } from "@/components/mode-toggle";
import { Activity, Bot, CircleAlert, CircleCheck, LoaderCircle, WifiOff } from "lucide-react";
import packageInfo from "../../package.json";

export function Header() {
  const { serverStatus, connectionStatus, agentType } = useChat();

  const agentStatus = {
    stable: {
      label: "Ready",
      detail: "Agent is ready",
      icon: CircleCheck,
      className: "text-emerald-600 dark:text-emerald-400",
    },
    running: {
      label: "Working",
      detail: "Agent is processing",
      icon: LoaderCircle,
      className: "text-amber-600 dark:text-amber-400",
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
  }[serverStatus];
  const status =
    connectionStatus === "offline"
      ? {
          label: "Offline",
          detail: "Network connection lost",
          icon: WifiOff,
          className: "text-destructive",
        }
      : connectionStatus === "reconnecting"
        ? {
            label: "Reconnecting",
            detail: "Reconnecting to agent server",
            icon: LoaderCircle,
            className: "text-amber-600 dark:text-amber-400",
          }
        : agentStatus;
  const StatusIcon = status.icon;

  return (
    <header className="z-20 flex h-16 shrink-0 items-center justify-between border-b bg-background/90 px-4 backdrop-blur-xl sm:px-6">
      <div className="flex min-w-0 items-center gap-3">
        <div className="grid size-9 shrink-0 place-items-center rounded-xl bg-foreground text-background shadow-sm">
          <Activity className="size-4" />
        </div>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-semibold tracking-tight">AgentAPI</span>
            <span
              className="rounded-full border bg-muted/60 px-2 py-0.5 font-mono text-[10px] font-medium text-muted-foreground"
              title="Build version"
            >
              v{packageInfo.version}
            </span>
            <span className="hidden rounded-full border bg-muted/60 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground sm:inline">
              Live session
            </span>
          </div>
          <p className="truncate text-xs text-muted-foreground">Remote coding agent workspace</p>
        </div>
      </div>

      <div className="flex items-center gap-2 sm:gap-3">
        {agentType !== "unknown" && (
          <div className="hidden items-center gap-2 rounded-full border bg-card px-3 py-1.5 text-xs font-medium shadow-xs sm:flex">
            <Bot className="size-3.5 text-muted-foreground" />
            <span>{AgentType[agentType].displayName}</span>
          </div>
        )}
        <div
          className={`flex items-center gap-2 rounded-full border bg-card px-3 py-1.5 text-xs font-medium shadow-xs ${status.className}`}
          title={status.detail}
        >
          <StatusIcon className={`size-3.5 ${
            serverStatus === "running" || connectionStatus === "reconnecting"
              ? "animate-spin"
              : ""
          }`} />
          <span>{status.label}</span>
        </div>
        <ModeToggle />
      </div>
    </header>
  );
}
