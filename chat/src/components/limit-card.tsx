"use client";

import { useEffect, useState } from "react";
import { Gauge } from "lucide-react";
import {useChatActions, useChatSelector} from "./chat-provider";
import { DecisionCardFooter, DecisionCardShell, PrimaryButton, SecondaryButton } from "./decision-card";
import type { ContinueAt, LimitStop } from "@/lib/chat-api";
import { formatMessageTime } from "@/lib/format-time";
import { queuedWaiting } from "@/lib/queue";
import { useAction } from "@/lib/use-action";
import { formatResetIn } from "@/lib/usage";

// The agent's last turn stopped on a usage limit (native transport): the
// queued tasks wait until the limit resets. The card says when, and offers
// to continue the task then (or now), or to let the queue go at once.
export function LimitCard() {
  const limitStop = useChatSelector((chat) => chat.limitStop);
  const queued = useChatSelector((chat) => chat.queuedMessages.length);
  const {continueAfterLimit, cancelContinue, dismissLimitStop} = useChatActions();
  // A new stop starts without the last one's error.
  const {busy, error, run} = useAction(limitStop?.stopped_at);
  // The countdown moves on while the card shows.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!limitStop) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, [limitStop]);

  if (!limitStop) return null;
  return (
    <LimitStopCard
      limitStop={limitStop}
      queued={queued}
      now={now}
      busy={busy}
      error={error}
      onContinue={(at) => void run(() => continueAfterLimit(at))}
      onCancelContinue={() => void run(cancelContinue)}
      onDismiss={() => void run(dismissLimitStop)}
    />
  );
}

// LimitStopCard is the card itself, without the chat it reads.
export function LimitStopCard({
  limitStop,
  queued,
  now,
  busy = false,
  error = "",
  onContinue,
  onCancelContinue,
  onDismiss,
}: {
  limitStop: LimitStop;
  queued: number;
  now: number;
  busy?: boolean;
  error?: string;
  onContinue?: (at: ContinueAt) => void;
  onCancelContinue?: () => void;
  onDismiss?: () => void;
}) {
  const resetsAt = limitStop.resets_at;
  const continueAt = limitStop.continue_at;
  const when = resetsAt
    ? `The limit resets ${formatResetIn(resetsAt, now)}, at ${formatMessageTime(resetsAt)}.`
    : "The agent didn't say when the limit resets.";
  return (
    <DecisionCardShell
      data-limit-stop
      icon={Gauge}
      label="Usage limit"
      title="The account reached its usage limit"
      description={limitStop.message}
      footer={
        <DecisionCardFooter status={continueAt ? `Continues the task at ${formatMessageTime(continueAt)}.` : ""} error={error}>
          {continueAt ? (
            <SecondaryButton onClick={onCancelContinue} disabled={busy} className="min-h-9 px-3">
              Don&apos;t continue
            </SecondaryButton>
          ) : resetsAt ? (
            <PrimaryButton onClick={() => onContinue?.("reset")} disabled={busy} className="min-h-9 px-3">
              Continue at reset
            </PrimaryButton>
          ) : null}
          <SecondaryButton onClick={() => onContinue?.("now")} disabled={busy} className="min-h-9 px-3">
            Continue now
          </SecondaryButton>
          <SecondaryButton
            onClick={onDismiss}
            disabled={busy}
            className="min-h-9 px-3"
            title={queued ? "Send the queued tasks as soon as the agent can take them" : "Hide this card"}
          >
            {queued ? "Send queued tasks" : "Dismiss"}
          </SecondaryButton>
        </DecisionCardFooter>
      }
    >
      <p className="px-3.5 pb-1 pt-1 text-[12.5px] text-muted-foreground">
        {when}
        {queuedWaiting(queued, continueAt ? "for the task to go on" : resetsAt ? "until then" : "until you go on")}
      </p>
    </DecisionCardShell>
  );
}
