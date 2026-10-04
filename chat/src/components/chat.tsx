"use client";

import {useEffect, useState} from "react";
import {ExternalLink, FolderSearch, RefreshCw, TerminalSquare} from "lucide-react";
import {useChat} from "./chat-provider";
import MessageInput from "./message-input";
import MessageList from "./message-list";
import dynamic from "next/dynamic";
import {TtyView} from "./tty-view";
import {DecisionCard, StateStrip} from "./dock";

// The Explorer (task index, MCP and Temporal settings) isn't needed for the
// first paint, so it loads right after, keeping the initial bundle small.
// The placeholder matches its trigger button.
const Explorer = dynamic(() => import("./explorer").then((module) => module.Explorer), {
  ssr: false,
  loading: () => (
    <span aria-hidden="true" className="grid size-8 place-items-center rounded-full text-muted-foreground">
      <FolderSearch className="size-4" />
    </span>
  ),
});
import {Button} from "./ui/button";
import {KeyboardShortcutsDialog, useKeyboardShortcutsKey} from "./keyboard-shortcuts";

export function Chat() {
  const [suggestedPrompt, setSuggestedPrompt] = useState("");
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [ttyMode, setTtyMode] = useState(false);
  useKeyboardShortcutsKey(() => setShortcutsOpen(true));
  const {
    messages,
    richMessages,
    loading,
    sendMessage,
    serverStatus,
    agentType,
    retryFailedMessage,
    dismissFailedMessage,
    connectionStatus,
    reconnectAttempt,
    nextReconnectAt,
    reconnectNow,
    storageScope,
    workspaceUrl,
  } = useChat();
  const [reconnectSeconds, setReconnectSeconds] = useState(0);
  const [coderWorkspaceUrl, setCoderWorkspaceUrl] = useState(workspaceUrl);

  useEffect(() => {
    if (workspaceUrl) return;
    let active = true;
    fetch(`${storageScope}/workspace`)
      .then((response) => response.ok ? response.json() as Promise<{url?: string}> : null)
      .then((value) => { if (active && value?.url) setCoderWorkspaceUrl(value.url); })
      .catch(() => undefined);
    return () => { active = false; };
  }, [storageScope, workspaceUrl]);

  useEffect(() => {
    if (!nextReconnectAt) {
      setReconnectSeconds(0);
      return;
    }
    const update = () =>
      setReconnectSeconds(
        Math.max(0, Math.ceil((nextReconnectAt - Date.now()) / 1000)),
      );
    update();
    const timer = window.setInterval(update, 250);
    return () => window.clearInterval(timer);
  }, [nextReconnectAt]);

  return (
    <section className="relative flex min-h-0 flex-1 flex-col">
      <div className="sr-only" aria-live="polite" aria-atomic="true">
        {serverStatus === "running"
          ? "Agent is working"
          : serverStatus === "stable"
            ? "Agent is ready"
            : "Agent connection is unavailable"}
        . {messages.length} conversation updates.
      </div>
      {connectionStatus !== "connected" && (
        <div
          className="flex min-h-10 shrink-0 items-center justify-center gap-3 border-b bg-status-warning/10 px-3 py-1.5 text-xs text-status-warning"
          role="status"
        >
          <span>
            {connectionStatus === "offline"
              ? "Network connection is offline."
              : reconnectSeconds > 0
                ? `Reconnect attempt ${reconnectAttempt} in ${reconnectSeconds}s.`
                : "Connecting to the agent server…"}
          </span>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-7 bg-background/80"
            onClick={reconnectNow}
            disabled={connectionStatus === "offline"}
          >
            <RefreshCw />
            Reconnect now
          </Button>
        </div>
      )}
      {ttyMode ? (
        <TtyView onExit={() => setTtyMode(false)} />
      ) : (
        <>
          <MessageList
            messages={messages}
            richMessages={richMessages}
            serverStatus={serverStatus}
            agentType={agentType}
            onRetryMessage={retryFailedMessage}
            onEditMessage={(clientId, content) => {
              dismissFailedMessage(clientId);
              setSuggestedPrompt(content);
            }}
            onDismissMessage={dismissFailedMessage}
            onStopTask={() => void sendMessage("\x1b", "raw")}
            onSendRaw={(data) => void sendMessage(data, "raw")}
            headerAction={
              <div className="flex items-center gap-0.5">
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-8 rounded-full text-muted-foreground"
                  title="TTY mode: use the agent's terminal directly"
                  aria-label="Switch to TTY mode"
                  onClick={() => setTtyMode(true)}
                >
                  <TerminalSquare className="size-4" />
                </Button>
                <Explorer
                  onNavigateTask={(number) => {
                    window.requestAnimationFrame(() =>
                      document.getElementById(`task-${number}`)?.scrollIntoView({
                        behavior: "smooth",
                        block: "start",
                      }),
                    );
                  }}
                />
                {coderWorkspaceUrl && (
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="hidden size-8 rounded-full text-muted-foreground sm:inline-flex"
                    title="Open Coder workspace"
                    aria-label="Open Coder workspace"
                    onClick={() => window.open(coderWorkspaceUrl, "_blank", "noopener,noreferrer")}
                  >
                    <ExternalLink className="size-4" />
                  </Button>
                )}
              </div>
            }
          />
          <MessageInput
            dock={
              <>
                <StateStrip />
                <DecisionCard onOpenTerminal={() => setTtyMode(true)} />
              </>
            }
            onSendMessage={sendMessage}
            disabled={loading}
            serverStatus={serverStatus}
            suggestedPrompt={suggestedPrompt}
            onSuggestedPromptApplied={() => setSuggestedPrompt("")}
          />
        </>
      )}
      <KeyboardShortcutsDialog open={shortcutsOpen} onOpenChange={setShortcutsOpen} />
    </section>
  );
}
