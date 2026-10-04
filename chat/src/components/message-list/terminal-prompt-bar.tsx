"use client";

import { useMemo } from "react";
import { Keyboard } from "lucide-react";
import { useChat } from "../chat-provider";
import { parseTerminalOptions, terminalOptionKeystrokes } from "@/lib/terminal-option";

// Shown while the server reports that the agent is waiting on an interactive
// prompt. The server decides from the live terminal (input box gone, prompt
// at the bottom), so a numbered list in an answer never shows this.
export function TerminalPromptBar({ onSendRaw }: { onSendRaw?: (data: string) => void }) {
  const { terminalPrompt, agentType } = useChat();
  const options = useMemo(() => parseTerminalOptions(terminalPrompt), [terminalPrompt]);
  if (!terminalPrompt) return null;

  return (
    <div
      className="rounded-lg border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs"
      role="alert"
    >
      <div className="flex items-center gap-2 text-status-warning">
        <Keyboard className="size-3.5 shrink-0" />
        <span>
          {options.length > 0
            ? "Select an option or switch to Terminal tab for more control."
            : "This prompt requires terminal input. Switch to the Terminal tab below to respond."}
        </span>
      </div>
      {options.length > 0 && onSendRaw && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {options.map((opt, i) => (
            <button
              key={`${opt.key}-${i}`}
              type="button"
              onClick={() => onSendRaw(terminalOptionKeystrokes(agentType, opt.key))}
              className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2.5 py-1.5 text-xs font-medium text-foreground shadow-sm transition hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {/^\d+$/.test(opt.key) && (
                <span className="grid size-5 place-items-center rounded bg-muted text-[10px] font-bold">{opt.key}</span>
              )}
              <span className="max-w-52 truncate">{opt.label}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
