"use client";

import { useEffect, useMemo, useState } from "react";
import { Check, TerminalSquare } from "lucide-react";
import { useChat } from "./chat-provider";
import { computeTokenTotals, formatTokenCount, getAgentState } from "@/lib/session-status";
import { useWorkingElapsed } from "@/lib/use-elapsed";
import { MULTI_SELECT_CONTINUE, describeTerminalPrompt, isMultiSelect, parseTerminalOptions, terminalOptionKeystrokes } from "@/lib/terminal-option";
import { getToolCommand } from "@/lib/tool-format";
import { splitThinking } from "@/lib/thinking";

// The strip above the composer: what the agent is doing right now. It only
// shows while the agent works or waits for you; a ready agent needs no
// chrome.
export function StateStrip() {
  const { serverStatus, connectionStatus, terminalPrompt, messages, richMessages, queuedMessages } = useChat();
  const kind = getAgentState(serverStatus, connectionStatus, terminalPrompt);
  const elapsed = useWorkingElapsed(serverStatus);

  const { activity, tokens } = useMemo(() => {
    const taskStart = messages.findLast((message) => message.role === "user")?.time;
    const startMs = taskStart ? Date.parse(taskStart) : 0;
    const current = richMessages.filter((message) => !startMs || Date.parse(message.timestamp) >= startMs);
    const finished = new Set<string>();
    for (const message of current) {
      for (const block of message.content) {
        if (block.type === "tool_result" && block.tool_use_id) finished.add(block.tool_use_id);
      }
    }
    let activity = "";
    for (const message of [...current].reverse()) {
      for (const block of [...message.content].reverse()) {
        if (block.type === "tool_use" && block.tool_use_id && !finished.has(block.tool_use_id)) {
          const command = getToolCommand(block.tool_name ?? "", block.tool_input);
          activity = command ? `$ ${command.split("\n")[0]}` : `${block.tool_name ?? "Tool"}`;
        } else if (block.type === "thinking" && block.thinking) {
          activity = `Thinking · ${splitThinking(block.thinking).title}`;
        }
        if (activity) break;
      }
      if (activity) break;
    }
    return { activity, tokens: computeTokenTotals(current) };
  }, [messages, richMessages]);

  if (kind !== "working" && kind !== "needs-you") return null;
  const needs = kind === "needs-you";
  return (
    <div className="grid gap-1.5" data-state-kind={kind}>
      {!needs && <div aria-hidden="true" className="state-scan h-px overflow-hidden rounded-full bg-border" />}
      <div className="flex min-w-0 items-center gap-3 px-1 text-xs tabular-nums text-muted-foreground" role="status">
        <span className={`flex shrink-0 items-center gap-1.5 font-semibold ${needs ? "text-state-needs" : "text-state-working"}`}>
          <span aria-hidden="true" className="state-led size-1.5 rounded-full bg-current" />
          {needs ? "Needs you" : "Working"}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-[11.5px]">
          {needs
            ? `The agent is waiting for your answer${queuedMessages.length ? ` · ${queuedMessages.length} queued ${queuedMessages.length === 1 ? "task waits" : "tasks wait"}` : ""}`
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
  const { terminalPrompt, sendTerminalInput, agentType } = useChat();
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

  useEffect(() => {
    if (!terminalPrompt || options.length === 0) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName) || target.closest(".xterm"))) return;
      const index = Number(event.key) - 1;
      if (Number.isInteger(index) && index >= 0 && index < options.length) {
        event.preventDefault();
        choose(options[index].key);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (!terminalPrompt) return null;
  return (
    <section
      aria-label="The agent is asking"
      className="decision-card overflow-hidden rounded-xl border border-state-needs/45 bg-card shadow-lg shadow-black/5"
    >
      <header className="flex items-start gap-3 px-3.5 pb-1 pt-3">
        <h2 className="min-w-0 flex-1 text-sm font-semibold leading-5">{title}</h2>
        <span className="hidden shrink-0 items-center gap-1 pt-0.5 text-[11px] text-muted-foreground sm:flex">
          {options.length > 0 && options.every((option) => /^\d+$/.test(option.key)) && (
            <>
              <kbd className="rounded border border-b-2 px-1 font-mono text-[10px]">1</kbd>–
              <kbd className="rounded border border-b-2 px-1 font-mono text-[10px]">{multi ? options.filter((option) => option.checked !== undefined).length : options.length}</kbd> {multi ? "to toggle" : "to choose"}
            </>
          )}
        </span>
      </header>
      {context.length > 0 && (
        <pre className="mx-3.5 mb-2 mt-1.5 max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-md bg-term px-3 py-2 font-mono text-xs leading-[1.55] text-term-foreground [overflow-wrap:anywhere]">
          {context.slice(0, 8).join("\n")}
        </pre>
      )}
      <div className="grid gap-1.5 px-3.5 pb-3">
        {options.length > 0 ? (
          options.map((option, index) => (
            <button
              key={`${option.key}-${index}`}
              type="button"
              disabled={sent}
              role={option.checked === undefined ? undefined : "checkbox"}
              aria-checked={option.checked}
              onClick={() => choose(option.key)}
              className="flex min-h-10 items-center gap-3 rounded-lg border px-2.5 py-1.5 text-left text-[13px] outline-none transition hover:border-state-needs/60 hover:bg-state-needs/10 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
            >
              <kbd className="grid min-w-5 place-items-center rounded border border-b-2 bg-background px-1 font-mono text-[10.5px] text-muted-foreground">
                {/^\d+$/.test(option.key) ? option.key : index + 1}
              </kbd>
              <span className="min-w-0 flex-1">{option.label}</span>
              {option.checked !== undefined && (
                <span
                  aria-hidden="true"
                  className={`grid size-4 shrink-0 place-items-center rounded border ${option.checked ? "border-state-needs bg-state-needs text-background" : "border-muted-foreground/50"}`}
                >
                  {option.checked && <Check className="size-3" strokeWidth={3} />}
                </span>
              )}
            </button>
          ))
        ) : (
          <p className="text-xs text-muted-foreground">This prompt needs typed input.</p>
        )}
        {multi && (
          <button
            type="button"
            disabled={sent}
            onClick={continueMulti}
            className="min-h-10 rounded-lg bg-state-needs px-3 text-[13px] font-semibold text-background outline-none transition hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
          >
            Continue
          </button>
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
    </section>
  );
}
