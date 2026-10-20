"use client";

import {useCallback, useEffect, useState} from "react";
import {ExternalLink, FolderSearch, RefreshCw, TerminalSquare} from "lucide-react";
import {useChat} from "./chat-provider";
import {useStableCallback} from "@/lib/use-stable-callback";
import MessageInput from "./message-input";
import MessageList from "./message-list";
import dynamic from "next/dynamic";
import {TtyView} from "./tty-view";
import {ShellView} from "./shell-view";
import {OPEN_TERMINAL_EVENT} from "@/lib/slash-commands";
import {DecisionCard, StateStrip} from "./dock";
import {RequestCard} from "./request-card";
import {LoginCard} from "./login-card";
import {LimitCard} from "./limit-card";
import {BackgroundAlerts} from "./background-alerts";

// The Explorer (task index, MCP settings) isn't needed for the
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
import {KeyboardShortcutsDialog, OPEN_SHORTCUTS_EVENT, useKeyboardShortcutsKey} from "./keyboard-shortcuts";

export function Chat() {
  const [suggestedPrompt, setSuggestedPrompt] = useState("");
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const openShortcutsDialog = useCallback(() => setShortcutsOpen(true), []);
  const [terminalOpen, setTerminalOpen] = useState(false);
  // What the native terminal types once it opens (the login command).
  const [shellCommand, setShellCommand] = useState<string | undefined>();
  useKeyboardShortcutsKey(openShortcutsDialog);
  useEffect(() => {
    window.addEventListener(OPEN_SHORTCUTS_EVENT, openShortcutsDialog);
    return () => window.removeEventListener(OPEN_SHORTCUTS_EVENT, openShortcutsDialog);
  }, [openShortcutsDialog]);
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
    workspaceUrl,
    features,
    canStop,
    stop,
  } = useChat();
  // Only the PTY transport has the agent's terminal: TTY mode, raw keys and
  // the terminal prompt card need it. A native agent has none; the server
  // offers a shell in its working directory instead.
  const hasTerminal = features.agentTerminal;
  const hasShell = features.shell;
  // The transcript's tasks are memoized: their handlers keep their identity.
  const editFailedMessage = useStableCallback((clientId: string, content: string) => {
    dismissFailedMessage(clientId);
    setSuggestedPrompt(content);
  });
  const stopTask = useStableCallback(() => void stop());
  const sendRaw = useStableCallback((data: string) => void sendMessage(data, "raw"));
  useEffect(() => {
    if (!hasTerminal && !hasShell) setTerminalOpen(false);
  }, [hasTerminal, hasShell]);
  const openShell = (command?: string) => {
    setShellCommand(command);
    setTerminalOpen(true);
  };
  // The /terminal and /login commands.
  useEffect(() => {
    if (!hasShell) return;
    const onOpen = (event: Event) => {
      setShellCommand((event as CustomEvent<string | undefined>).detail);
      setTerminalOpen(true);
    };
    window.addEventListener(OPEN_TERMINAL_EVENT, onOpen);
    return () => window.removeEventListener(OPEN_TERMINAL_EVENT, onOpen);
  }, [hasShell]);
  const [reconnectSeconds, setReconnectSeconds] = useState(0);

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
      {terminalOpen && hasTerminal ? (
        <TtyView onExit={() => setTerminalOpen(false)} />
      ) : terminalOpen && hasShell ? (
        <ShellView onExit={() => setTerminalOpen(false)} command={shellCommand} />
      ) : (
        <>
          <MessageList
            messages={messages}
            richMessages={richMessages}
            serverStatus={serverStatus}
            agentType={agentType}
            onRetryMessage={retryFailedMessage}
            onEditMessage={editFailedMessage}
            onDismissMessage={dismissFailedMessage}
            onStopTask={canStop ? stopTask : undefined}
            onSendRaw={hasTerminal ? sendRaw : undefined}
            headerAction={
              <div className="flex items-center gap-0.5">
                {hasTerminal && (
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="size-8 rounded-full text-muted-foreground"
                    title="TTY mode: use the agent's terminal directly"
                    aria-label="Switch to TTY mode"
                    onClick={() => setTerminalOpen(true)}
                  >
                    <TerminalSquare className="size-4" />
                  </Button>
                )}
                {hasShell && (
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="size-8 rounded-full text-muted-foreground"
                    title="Terminal: a shell in the agent's working directory"
                    aria-label="Open the terminal"
                    onClick={() => openShell()}
                  >
                    <TerminalSquare className="size-4" />
                  </Button>
                )}
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
                {workspaceUrl && (
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="size-8 rounded-full text-muted-foreground"
                    title="Open Coder workspace"
                    aria-label="Open Coder workspace"
                    onClick={() => window.open(workspaceUrl, "_blank", "noopener,noreferrer")}
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
                {hasTerminal && <DecisionCard onOpenTerminal={() => setTerminalOpen(true)} />}
                {features.requests && <LoginCard onRunInTerminal={hasShell ? openShell : undefined} />}
                {features.requests && <RequestCard />}
                {features.requests && <LimitCard />}
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
      <BackgroundAlerts />
    </section>
  );
}
