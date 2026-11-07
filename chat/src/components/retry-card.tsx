"use client";

import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import {useChatActions, useChatSelector} from "./chat-provider";
import { CardStatus, DecisionCardShell, PrimaryButton, SecondaryButton } from "./decision-card";
import type { RetryWait } from "@/lib/chat-api";
import { getErrorMessage } from "@/lib/error-utils";
import { retryCountdown, retryTitle } from "@/lib/retry";

// The agent's last turn failed on an upstream error that may pass (native
// transport): AgentAPI tries it again by itself, and the queued tasks wait
// for that. The card counts down to the retry, and offers to retry now or
// not at all.
export function RetryCard() {
  const retryWait = useChatSelector((chat) => chat.retryWait);
  const queued = useChatSelector((chat) => chat.queuedMessages.length);
  const {retryNow, cancelRetry} = useChatActions();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // The countdown moves on every second while the card shows.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!retryWait) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [retryWait]);
  // A new failure starts without the last one's error.
  useEffect(() => setError(""), [retryWait?.stopped_at]);

  if (!retryWait) return null;

  const act = async (action: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (failure) {
      setError(getErrorMessage(failure));
    } finally {
      setBusy(false);
    }
  };

  return (
    <RetryWaitCard
      retryWait={retryWait}
      queued={queued}
      now={now}
      busy={busy}
      error={error}
      onRetryNow={() => void act(retryNow)}
      onCancel={() => void act(cancelRetry)}
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
  const waiting = queued ? ` ${queued} queued ${queued === 1 ? "task waits" : "tasks wait"} for it.` : "";
  return (
    <DecisionCardShell
      data-retry-wait
      icon={RefreshCw}
      label="Retrying"
      title={retryTitle(retryWait)}
      description={retryWait.message}
      footer={
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3.5 pb-3 pt-2">
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
          <CardStatus className="min-w-0 flex-1 basis-48 text-[11.5px]" status="" error={error} />
        </div>
      }
    >
      <p className="px-3.5 pb-1 pt-1 text-[12.5px] text-muted-foreground">
        {retryCountdown(retryWait, now)}
        {waiting}
      </p>
    </DecisionCardShell>
  );
}
