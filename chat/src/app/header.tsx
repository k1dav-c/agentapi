"use client";

import { type ComponentType, useEffect, useMemo, useState } from "react";
import { AgentType, useChat } from "@/components/chat-provider";
import { ModeToggle } from "@/components/mode-toggle";
import { Activity, Bot, CircleAlert, CircleCheck, Download, LoaderCircle, WifiOff } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

export function Header() {
  const {
    serverStatus,
    connectionStatus,
    agentType,
    queuedMessages,
    richMessages,
    messages,
    downloadSession,
  } = useChat();
  const [runningSince, setRunningSince] = useState<number | null>(null);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [downloading, setDownloading] = useState(false);

  useEffect(() => {
    if (serverStatus !== "running") {
      setRunningSince(null);
      setElapsedSeconds(0);
      return;
    }
    setRunningSince((current) => current ?? Date.now());
  }, [serverStatus]);

  useEffect(() => {
    if (runningSince === null) return;
    const updateElapsed = () =>
      setElapsedSeconds(Math.floor((Date.now() - runningSince) / 1000));
    updateElapsed();
    const timer = window.setInterval(updateElapsed, 1000);
    return () => window.clearInterval(timer);
  }, [runningSince]);

  const latestTool = useMemo(() => {
    const latestTaskTime = [...messages]
      .reverse()
      .find((message) => message.role === "user")?.time;
    for (const message of [...richMessages].reverse()) {
      if (
        latestTaskTime &&
        message.timestamp &&
        Date.parse(message.timestamp) < Date.parse(latestTaskTime)
      ) {
        continue;
      }
      for (const block of [...message.content].reverse()) {
        if (block.type === "tool_use" && block.tool_name) return block.tool_name;
      }
    }
    return null;
  }, [messages, richMessages]);

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
  const elapsed =
    elapsedSeconds < 60
      ? `${elapsedSeconds}s`
      : `${Math.floor(elapsedSeconds / 60)}m ${elapsedSeconds % 60}s`;
  const activityDetail =
    serverStatus === "running"
      ? [latestTool ? `Using ${latestTool}` : "Processing task", elapsed]
          .filter(Boolean)
          .join(" · ")
      : status.detail;

  return (
    <header className="z-20 flex h-[calc(3.5rem+env(safe-area-inset-top))] shrink-0 items-center justify-between border-b bg-background/90 px-3 pt-[env(safe-area-inset-top)] backdrop-blur-xl sm:h-16 sm:px-6 sm:pt-0">
      <div className="flex min-w-0 items-center gap-2.5 sm:gap-3">
        <div className="hidden size-9 shrink-0 place-items-center rounded-xl bg-foreground text-background shadow-sm min-[380px]:grid">
          <Activity className="size-4" />
        </div>
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-semibold tracking-tight">AgentAPI</span>
          </div>
          <p className="hidden truncate text-xs text-muted-foreground sm:block">
            {agentType === "unknown"
              ? "Remote coding agent"
              : AgentType[agentType].displayName}
          </p>
        </div>
      </div>

      <div className="flex items-center gap-2 sm:gap-3">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              className={`relative flex min-h-10 items-center gap-1.5 rounded-full border bg-card px-2.5 text-xs font-medium shadow-xs outline-none transition hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring sm:gap-2 sm:px-3 ${status.className}`}
              title={`${activityDetail}. Open session details`}
              aria-label={`${status.label}. Open session details`}
            >
              <StatusIcon className={`size-3.5 ${
                serverStatus === "running" || connectionStatus === "reconnecting"
                  ? "motion-safe:animate-spin"
                  : ""
              }`} />
              <span className="hidden min-[380px]:inline">{status.label}</span>
              {queuedMessages.length > 0 && (
                <span className="grid min-w-5 place-items-center rounded-full bg-muted px-1.5 py-0.5 text-[10px] text-foreground">
                  {queuedMessages.length}
                  <span className="sr-only"> queued tasks</span>
                </span>
              )}
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-64 p-2">
            <DropdownMenuLabel>Session details</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <div className="space-y-3 px-2 py-2 text-xs">
              <SessionDetail
                icon={StatusIcon}
                label="Status"
                value={status.label}
                valueClassName={status.className}
              />
              <SessionDetail
                icon={Bot}
                label="Agent"
                value={
                  agentType === "unknown"
                    ? "Unknown"
                    : AgentType[agentType].displayName
                }
              />
              <SessionDetail
                icon={Activity}
                label="Activity"
                value={activityDetail}
              />
              <SessionDetail
                icon={CircleCheck}
                label="Queue"
                value={`${queuedMessages.length} ${queuedMessages.length === 1 ? "task" : "tasks"}`}
              />
            </div>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              disabled={downloading}
              onSelect={() => {
                setDownloading(true);
                void downloadSession()
                  .catch(() => {
                    // The provider reports request failures through the
                    // rejected promise; keep the menu action retryable.
                  })
                  .finally(() => setDownloading(false));
              }}
            >
              {downloading ? (
                <LoaderCircle className="motion-safe:animate-spin" />
              ) : (
                <Download />
              )}
              Download session JSONL
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <ModeToggle />
      </div>
    </header>
  );
}

function SessionDetail({
  icon: Icon,
  label,
  value,
  valueClassName = "",
}: {
  icon: ComponentType<{className?: string}>;
  label: string;
  value: string;
  valueClassName?: string;
}) {
  return (
    <div className="grid grid-cols-[1rem_4rem_1fr] items-start gap-2">
      <Icon className="mt-0.5 size-3.5 text-muted-foreground" />
      <span className="text-muted-foreground">{label}</span>
      <span className={`min-w-0 break-words text-right font-medium ${valueClassName}`}>
        {value}
      </span>
    </div>
  );
}
