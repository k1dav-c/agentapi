"use client";

import { useEffect, useState } from "react";
import { Gauge } from "lucide-react";
import {useChatActions, useChatSelector} from "./chat-provider";
import { CardStatus, DecisionCardShell, PrimaryButton, SecondaryButton } from "./decision-card";
import { getErrorMessage } from "@/lib/error-utils";
import { formatMessageTime } from "@/lib/format-time";
import { formatResetIn } from "@/lib/usage";

// The agent's last turn stopped on a usage limit (native transport): the
// queued tasks wait until the limit resets. The card says when, and offers
// to continue the task then (or now), or to let the queue go at once.
export function LimitCard() {
  const limitStop = useChatSelector((chat) => chat.limitStop);
  const queuedMessages = useChatSelector((chat) => chat.queuedMessages);
  const {continueAfterLimit, cancelContinue, dismissLimitStop} = useChatActions();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // The countdown moves on while the card shows.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!limitStop) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, [limitStop]);
  // A new stop starts without the last one's error.
  useEffect(() => setError(""), [limitStop?.stopped_at]);

  if (!limitStop) return null;
  const resetsAt = limitStop.resets_at;
  const continueAt = limitStop.continue_at;
  const queued = queuedMessages.length;

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

  const when = resetsAt
    ? `The limit resets ${formatResetIn(resetsAt, now)}, at ${formatMessageTime(resetsAt)}.`
    : "The agent didn't say when the limit resets.";
  const waiting = queued
    ? ` ${queued} queued ${queued === 1 ? "task waits" : "tasks wait"} ${continueAt ? "for the task to go on" : resetsAt ? "until then" : "until you go on"}.`
    : "";

  return (
    <DecisionCardShell
      data-limit-stop
      icon={Gauge}
      label="Usage limit"
      title="The account reached its usage limit"
      description={limitStop.message}
      footer={
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3.5 pb-3 pt-2">
          {continueAt ? (
            <SecondaryButton onClick={() => void act(cancelContinue)} disabled={busy} className="min-h-9 px-3">
              Don&apos;t continue
            </SecondaryButton>
          ) : resetsAt ? (
            <PrimaryButton onClick={() => void act(() => continueAfterLimit("reset"))} disabled={busy} className="min-h-9 px-3">
              Continue at reset
            </PrimaryButton>
          ) : null}
          <SecondaryButton onClick={() => void act(() => continueAfterLimit("now"))} disabled={busy} className="min-h-9 px-3">
            Continue now
          </SecondaryButton>
          <SecondaryButton
            onClick={() => void act(dismissLimitStop)}
            disabled={busy}
            className="min-h-9 px-3"
            title={queued ? "Send the queued tasks as soon as the agent can take them" : "Hide this card"}
          >
            {queued ? "Send queued tasks" : "Dismiss"}
          </SecondaryButton>
          <CardStatus
            className="min-w-0 flex-1 basis-48 text-[11.5px]"
            status={continueAt ? `Continues the task at ${formatMessageTime(continueAt)}.` : ""}
            error={error}
          />
        </div>
      }
    >
      <p className="px-3.5 pb-1 pt-1 text-[12.5px] text-muted-foreground">
        {when}
        {waiting}
      </p>
    </DecisionCardShell>
  );
}
