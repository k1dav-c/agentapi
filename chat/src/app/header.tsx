"use client";

import { type ComponentType, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { notificationsEnabled, onNotificationsPreferenceChange, setNotificationsEnabled } from "@/lib/background-alerts";
import { AgentType, useChat } from "@/components/chat-provider";
import { ModeToggle } from "@/components/mode-toggle";
import { AgentMark } from "@/components/agent-mark";
import { Activity, BellOff, BellRing, Bot, MessageSquareText, CircleCheck, Download, Hash, Keyboard, LoaderCircle, Tag } from "lucide-react";
import { computeTokenTotals, formatTokenCount, getStatusMeta } from "@/lib/session-status";
import { useWorkingElapsed } from "@/lib/use-elapsed";
import { KeyboardShortcutsDialog, useKeyboardShortcutsKey } from "@/components/keyboard-shortcuts";
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
    terminalPrompt,
    agentType,
    queuedMessages,
    richMessages,
    messages,
    downloadSession,
    customTitle,
    agentapiVersion,
    sessionName,
  } = useChat();
  const [downloading, setDownloading] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  useKeyboardShortcutsKey(() => setShortcutsOpen(true));
  const elapsed = useWorkingElapsed(serverStatus);
  const [notify, setNotify] = useState(false);
  useEffect(() => {
    const sync = () => setNotify(notificationsEnabled() && typeof Notification !== "undefined" && Notification.permission === "granted");
    sync();
    return onNotificationsPreferenceChange(sync);
  }, []);
  // Notifications when the agent needs you or finishes while this tab is in
  // the background. Turning them on asks the browser for permission.
  const toggleNotifications = async () => {
    if (notify) {
      setNotificationsEnabled(false);
      return;
    }
    if (typeof Notification === "undefined") {
      toast.error("This browser can't show notifications here", {description: "They need a secure (https) page."});
      return;
    }
    const permission = Notification.permission === "default" ? await Notification.requestPermission() : Notification.permission;
    if (permission !== "granted") {
      toast.error("Notifications are blocked for this site", {description: "Allow them in the browser's site settings, then try again."});
      return;
    }
    setNotificationsEnabled(true);
    toast.success("You'll be notified when the agent needs you or finishes");
  };

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

  const tokenTotals = useMemo(() => computeTokenTotals(richMessages), [richMessages]);
  const status = getStatusMeta(serverStatus, connectionStatus, terminalPrompt);
  const StatusIcon = status.icon;
  const activityDetail =
    status.kind === "working"
      ? [latestTool ? `Using ${latestTool}` : "Processing task", elapsed].filter(Boolean).join(" · ")
      : status.detail;
  const agentName = agentType === "unknown" ? "Remote coding agent" : AgentType[agentType].displayName;
  const held = status.kind === "needs-you" && queuedMessages.length > 0;

  return (
    <header className="sticky top-[env(safe-area-inset-top,0px)] z-20 flex h-12 shrink-0 items-center gap-3 border-b bg-background/95 px-3 backdrop-blur-xl sm:px-5">
      <div className="flex min-w-0 flex-1 items-center gap-2.5">
        <AgentMark agentType={agentType} />
        {/* On a phone there is room for one name: the session's, next to the
            agent's mark. The agent name stays in the heading for screen
            readers and comes back on wider screens. */}
        <h1
          className={`text-sm font-semibold tracking-tight ${
            sessionName ? "sr-only sm:not-sr-only sm:max-w-[40%] sm:shrink-0 sm:truncate" : "truncate"
          }`}
        >
          {customTitle || agentName}
        </h1>
        {sessionName && (
          // What the agent calls this session, to tell sessions apart.
          <>
            <span aria-hidden="true" className="-mx-1 hidden shrink-0 text-sm text-muted-foreground/50 sm:inline">/</span>
            <span
              className="min-w-0 truncate text-sm font-medium text-foreground sm:font-normal sm:text-muted-foreground"
              title={sessionName}
              data-session-name
            >
              {sessionName}
            </span>
          </>
        )}
        {agentapiVersion && (
          <span className="hidden shrink-0 font-mono text-[11px] text-muted-foreground lg:inline">
            v{agentapiVersion}
          </span>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-1.5">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              data-state-kind={status.kind}
              className={`state-pill relative flex h-7 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full border px-2.5 text-xs font-medium tabular-nums outline-none transition hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring ${status.className}`}
              title={`${activityDetail}. Open session details`}
              aria-label={`${status.label}. Open session details`}
            >
              <span aria-hidden="true" className="state-led size-1.5 rounded-full bg-current" />
              <span>{status.label}</span>
              {status.kind === "working" && elapsed && (
                <span className="hidden text-muted-foreground min-[420px]:inline">{elapsed}</span>
              )}
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-72 p-2">
            <DropdownMenuLabel>Session details</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <div className="space-y-3 px-2 py-2 text-xs">
              <SessionDetail icon={StatusIcon} label="Status" value={status.label} valueClassName={status.className} />
              <SessionDetail icon={Bot} label="Agent" value={agentType === "unknown" ? "Unknown" : AgentType[agentType].displayName} />
              {sessionName && <SessionDetail icon={MessageSquareText} label="Session" value={sessionName} />}
              {agentapiVersion && <SessionDetail icon={Tag} label="Version" value={`AgentAPI v${agentapiVersion}`} />}
              <SessionDetail icon={Activity} label="Activity" value={activityDetail} />
              <SessionDetail
                icon={CircleCheck}
                label="Queue"
                value={`${queuedMessages.length} ${queuedMessages.length === 1 ? "task" : "tasks"}${held ? " · held" : ""}`}
              />
              {tokenTotals.total > 0 && (
                <SessionDetail
                  icon={Hash}
                  label="Tokens"
                  value={`${formatTokenCount(tokenTotals.input)} in · ${formatTokenCount(tokenTotals.output)} out`}
                />
              )}
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
              {downloading ? <LoaderCircle className="motion-safe:animate-spin" /> : <Download />}
              Download conversation JSONL
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={(event) => { event.preventDefault(); void toggleNotifications(); }}>
              {notify ? <BellRing /> : <BellOff />}
              {notify ? "Background notifications on" : "Notify me in the background"}
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setShortcutsOpen(true)}>
              <Keyboard />
              Keyboard shortcuts
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        {queuedMessages.length > 0 && (
          <span
            className={`hidden h-7 items-center rounded-full border px-2.5 text-xs tabular-nums sm:flex ${held ? "text-state-needs" : "text-muted-foreground"}`}
            title={held ? "Queued tasks run after you answer the agent" : "Tasks waiting to run"}
          >
            {queuedMessages.length} queued{held ? " · held" : ""}
          </span>
        )}
        {tokenTotals.total > 0 && (
          <span
            className="hidden h-7 items-center gap-1 rounded-full border px-2.5 text-xs tabular-nums text-muted-foreground md:flex"
            title={`${tokenTotals.input.toLocaleString()} input + ${tokenTotals.output.toLocaleString()} output tokens`}
          >
            {formatTokenCount(tokenTotals.total)} tokens
          </span>
        )}
        <div id={HEADER_ACTIONS_ID} className="flex items-center gap-0.5" />
        <ModeToggle />
      </div>
      <KeyboardShortcutsDialog open={shortcutsOpen} onOpenChange={setShortcutsOpen} />
    </header>
  );
}

// The page's own actions (search, TTY, Explorer) render into this slot so
// the app has a single top bar; see MessageList.
export const HEADER_ACTIONS_ID = "header-actions";

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
