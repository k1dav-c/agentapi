"use client";

import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import {useChatActions, useChatSelector} from "./chat-provider";
import { DecisionCardFooter, DecisionCardShell, PrimaryButton, SecondaryButton } from "./decision-card";
import type { RetryWait } from "@/lib/chat-api";
import { queuedWaiting } from "@/lib/queue";
import { retryCountdown, retryTitle } from "@/lib/retry";
import { useAction } from "@/lib/use-action";

// The agent's last turn failed on an upstream error that may pass (native
// transport): AgentAPI tries it again by itself, and the queued tasks wait
// for that. The card counts down to the retry, and offers to retry now or
// not at all.
export function RetryCard() {
  const retryWait = useChatSelector((chat) => chat.retryWait);
  const queued = useChatSelector((chat) => chat.queuedMessages.length);
  const {retryNow, cancelRetry} = useChatActions();
  const {busy, error, run} = useAction(retryWait?.stopped_at);
  // The countdown moves on every second while the card shows.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!retryWait) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [retryWait]);
  if (!retryWait) return null;

  return (
    <RetryWaitCard
      retryWait={retryWait}
      queued={queued}
      now={now}
      busy={busy}
      error={error}
      onRetryNow={() => void run(retryNow)}
      onCancel={() => void run(cancelRetry)}
    />
  );
}

// RetryWaitCard is the card itself, without the chat it reads.
export function RetryWaitCard({
  retryWait,
  queued,
  now,
  busy = false,
  error = "",
  onRetryNow,
  onCancel,
}: {
  retryWait: RetryWait;
  queued: number;
  now: number;
  busy?: boolean;
  error?: string;
  onRetryNow?: () => void;
  onCancel?: () => void;
}) {
  return (
    <DecisionCardShell
      data-retry-wait
      icon={RefreshCw}
      label="Retrying"
      title={retryTitle(retryWait)}
      description={retryWait.message}
      footer={
        <DecisionCardFooter error={error}>
          <PrimaryButton onClick={onRetryNow} disabled={busy} className="min-h-9 px-3">
            Retry now
          </PrimaryButton>
          <SecondaryButton
            onClick={onCancel}
            disabled={busy}
            className="min-h-9 px-3"
            title={queued ? "Don't try the task again; send the queued tasks" : "Don't try the task again"}
          >
            Cancel
          </SecondaryButton>
        </DecisionCardFooter>
      }
    >
      <p className="px-3.5 pb-1 pt-1 text-[12.5px] text-muted-foreground">
        {retryCountdown(retryWait, now)}
        {queuedWaiting(queued, "for it")}
      </p>
    </DecisionCardShell>
  );
}
