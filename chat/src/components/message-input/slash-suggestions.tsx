"use client";

import {completion, type SlashCommandInfo} from "@/lib/slash-commands";

// The chat's own slash commands that match what is typed, above the
// message: a click (or Tab, in the composer) completes one.
export function SlashSuggestions({commands, onComplete}: {commands: SlashCommandInfo[]; onComplete: (text: string) => void}) {
  if (commands.length === 0) return null;
  return (
    <div className="border-b bg-muted/30 px-2 py-1.5" role="group" aria-label="Chat commands" data-slash-suggestions>
      {commands.map((command) => (
        <button
          key={command.name}
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => onComplete(completion(command))}
          className="flex w-full items-baseline gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-muted"
        >
          <code className="shrink-0 font-mono text-foreground">/{command.name}</code>
          {command.args && <span className="shrink-0 font-mono text-muted-foreground max-sm:hidden">{command.args}</span>}
          <span className="min-w-0 truncate text-muted-foreground">{command.description}</span>
        </button>
      ))}
      <p className="px-2 pt-0.5 text-[11px] text-muted-foreground">Tab completes; other commands go to the agent.</p>
    </div>
  );
}
