"use client";

import {useEffect, useState} from "react";
import {RefreshCw} from "lucide-react";
import {useChat} from "./chat-provider";
import MessageInput from "./message-input";
import MessageList from "./message-list";
import {Explorer} from "./explorer";
import {Button} from "./ui/button";

export function Chat() {
  const [suggestedPrompt, setSuggestedPrompt] = useState("");
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
  } = useChat();
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
    <section className="relative flex min-h-0 flex-1 flex-col bg-[radial-gradient(circle_at_top,_var(--surface-glow),_transparent_42%)]">
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
          className="flex min-h-10 shrink-0 items-center justify-center gap-3 border-b bg-amber-500/10 px-3 py-1.5 text-xs text-amber-900 dark:text-amber-200"
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
      <div className="flex justify-end border-b bg-background/80 px-3 py-2 sm:px-6">
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
      </div>
      <MessageList
        messages={messages}
        richMessages={richMessages}
        serverStatus={serverStatus}
        agentType={agentType}
        onSelectPrompt={setSuggestedPrompt}
        onRetryMessage={retryFailedMessage}
        onEditMessage={(clientId, content) => {
          dismissFailedMessage(clientId);
          setSuggestedPrompt(content);
        }}
        onDismissMessage={dismissFailedMessage}
        onStopTask={() => void sendMessage("\x1b", "raw")}
      />
      <MessageInput
        onSendMessage={sendMessage}
        disabled={loading}
        serverStatus={serverStatus}
        suggestedPrompt={suggestedPrompt}
        onSuggestedPromptApplied={() => setSuggestedPrompt("")}
      />
    </section>
  );
}
