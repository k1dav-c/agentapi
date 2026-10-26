"use client";

import { useEffect, useMemo, useState } from "react";
import { TerminalSquare } from "lucide-react";
import {useChatActions, useChatSelector} from "./chat-provider";
import { ChoiceMark, DecisionCardShell, PrimaryButton, TONE_CLASS, TerminalBlock, optionRowClass } from "./decision-card";
import { Kbd } from "./ui/kbd";
import { sameValue } from "@/lib/agent-status";
import { formatTokenCount } from "@/lib/session-status";
import { useWorkingElapsed } from "@/lib/use-elapsed";
import { useNumberKeys } from "@/lib/use-number-keys";
import { MULTI_SELECT_CONTINUE, describeTerminalPrompt, isMultiSelect, parseTerminalOptions, terminalOptionKeystrokes } from "@/lib/terminal-option";
import { loginTitle } from "@/lib/login";

// The strip above the composer: what the agent is doing right now. It only
// shows while the agent works or waits for you; a ready agent needs no
// chrome. On a phone, waiting for you is left to the card that asks (it is
// still read out), as the screen has no room to say it twice.
export function StateStrip() {
  const kind = useChatSelector((chat) => chat.agentState);
  const serverStatus = useChatSelector((chat) => chat.serverStatus);
  const pendingRequestCount = useChatSelector((chat) => chat.pendingRequestCount);
  const loginRequired = useChatSelector((chat) => chat.loginRequired);
  const queuedCount = useChatSelector((chat) => chat.queuedMessages.length);
  // What the agent does in its task and the tokens it took, worked out from
  // the task's latest steps (useTranscript): the strip renders again when
  // they change, not whenever a message streams.
  const activity = useChatSelector((chat) => chat.taskActivity);
  const tokens = useChatSelector((chat) => chat.taskTokens, sameValue);
  const elapsed = useWorkingElapsed(serverStatus);

  if (kind !== "working" && kind !== "needs-you") return null;
  const needs = kind === "needs-you";
  return (
    <div className={needs ? "grid gap-1.5 max-sm:sr-only" : "grid gap-1.5"} data-state-kind={kind}>
      {!needs && <div aria-hidden="true" className="state-scan h-px overflow-hidden rounded-full bg-border" />}
      <div className="flex min-w-0 items-center gap-3 px-1 text-xs tabular-nums text-muted-foreground" role="status">
        <span className={`flex shrink-0 items-center gap-1.5 font-semibold ${needs ? "text-state-needs" : "text-state-working"}`}>
          <span aria-hidden="true" className="state-led size-1.5 rounded-full bg-current" />
          {needs ? "Needs you" : "Working"}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-[11.5px]">
          {needs
            ? `${loginRequired ? loginTitle(loginRequired) : `The agent is waiting for your answer${pendingRequestCount > 1 ? ` · ${pendingRequestCount} requests` : ""}`}${queuedCount ? ` · ${queuedCount} queued ${queuedCount === 1 ? "task waits" : "tasks wait"}` : ""}`
            : activity || "Working on the task"}
        </span>
        {!needs && elapsed && <span className="shrink-0">{elapsed}</span>}
        {!needs && tokens.total > 0 && (
          <span className="hidden shrink-0 sm:inline" title="Tokens used by this task">
            ↑{formatTokenCount(tokens.input + tokens.cacheRead)} ↓{formatTokenCount(tokens.output)}
          </span>
        )}
      </div>
    </div>
  );
}

// The agent's dialog as a card docked above the composer, instead of
// buried at the end of the transcript. Number keys pick an option.
export function DecisionCard({ onOpenTerminal }: { onOpenTerminal?: () => void }) {
  const terminalPrompt = useChatSelector((chat) => chat.terminalPrompt);
  const agentType = useChatSelector((chat) => chat.agentType);
  const {sendTerminalInput} = useChatActions();
  const [sentFor, setSentFor] = useState("");
  const options = useMemo(() => parseTerminalOptions(terminalPrompt), [terminalPrompt]);
  const { title, context } = useMemo(() => describeTerminalPrompt(terminalPrompt), [terminalPrompt]);
  const multi = isMultiSelect(options);
  const sent = sentFor !== "" && sentFor === terminalPrompt;
  // Forget the answer once the dialog closes, so the same question asked
  // again (e.g. the same permission for another command) can be answered.
  useEffect(() => {
    if (!terminalPrompt) setSentFor("");
  }, [terminalPrompt]);

  const choose = (key: string) => {
    if (sent) return;
    const option = options.find((candidate) => candidate.key === key);
    // A checkbox toggles and the question stays open, so it doesn't lock
    // the card; the redrawn prompt shows the new state.
    if (option?.checked === undefined) setSentFor(terminalPrompt);
    void sendTerminalInput(terminalOptionKeystrokes(agentType, key));
  };

  const continueMulti = () => {
    if (sent) return;
    setSentFor(terminalPrompt);
    void sendTerminalInput(MULTI_SELECT_CONTINUE);
  };

  useNumberKeys(terminalPrompt ? options.length : 0, (index) => choose(options[index].key));

  if (!terminalPrompt) return null;
  return (
    <DecisionCardShell
      title={title}
      aside={
        <span className="hidden shrink-0 items-center gap-1 pt-0.5 text-[11px] text-muted-foreground sm:flex">
          {options.length > 0 && options.every((option) => /^\d+$/.test(option.key)) && (
            <>
              <Kbd small>1</Kbd>–
              <Kbd small>{multi ? options.filter((option) => option.checked !== undefined).length : options.length}</Kbd> {multi ? "to toggle" : "to choose"}
            </>
          )}
        </span>
      }
      footer={
        <div className="grid gap-1.5 px-3.5 pb-3 pt-2">
          {options.length > 0 ? (
            options.map((option, index) => (
              <button
                key={`${option.key}-${index}`}
                type="button"
                disabled={sent}
                role={option.checked === undefined ? undefined : "checkbox"}
                aria-checked={option.checked}
                onClick={() => choose(option.key)}
                className={`${optionRowClass} ${TONE_CLASS.neutral}`}
              >
                <Kbd>{/^\d+$/.test(option.key) ? option.key : index + 1}</Kbd>
                <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{option.label}</span>
                {option.checked !== undefined && <ChoiceMark checked={option.checked} />}
              </button>
            ))
          ) : (
            <p className="text-xs text-muted-foreground">This prompt needs typed input.</p>
          )}
          {multi && (
            <PrimaryButton disabled={sent} onClick={continueMulti} className="min-h-10 px-3">
              Continue
            </PrimaryButton>
          )}
          <div className="flex items-center justify-between gap-3 pt-0.5 text-[11px] text-muted-foreground">
            <span role="status">{sent ? "Answer sent. Waiting for the agent…" : ""}</span>
            {onOpenTerminal && (
              <button type="button" onClick={onOpenTerminal} className="flex items-center gap-1 rounded outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring">
                <TerminalSquare className="size-3.5" />
                Answer in the terminal
              </button>
            )}
          </div>
        </div>
      }
    >
      {context.length > 0 && <TerminalBlock className="mx-3.5 mb-1 mt-1.5">{context.slice(0, 8).join("\n")}</TerminalBlock>}
    </DecisionCardShell>
  );
}
