"use client";

import {ChevronUp, CornerDownRight, ListPlus, OctagonX, SendIcon} from "lucide-react";
import {Button} from "../ui/button";
import {Kbd} from "../ui/kbd";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import type {ServerStatus} from "@/lib/agent-status";
import type {DispatchMode} from "@/lib/dispatch";

// The send button (it queues while the agent works), and with more than
// one way to send, a menu of them: queue, steer, interrupt and send.
export function SendButtonGroup({
  serverStatus,
  modes,
  sendBlocked,
  queuedCount,
  modKey,
  onSubmit,
}: {
  serverStatus: ServerStatus;
  modes: DispatchMode[];
  sendBlocked: boolean;
  queuedCount: number;
  // ⌘ or Ctrl, for the shortcut hints.
  modKey: string;
  onSubmit: (mode: DispatchMode) => void;
}) {
  return (
   <div className="flex items-center" data-send-group>
    <Button
      type="submit"
      disabled={sendBlocked}
      size="icon"
      className={`relative size-10 shadow-sm ${modes.length > 1 ? "rounded-l-full rounded-r-none" : "rounded-full"}`}
      title={
        serverStatus === "running"
          ? "Add task to queue"
          : "Send task"
      }
    >
      {serverStatus === "running" ? <ListPlus /> : <SendIcon />}
      {serverStatus === "running" && queuedCount > 0 && (
        <span
          aria-hidden="true"
          className="absolute -right-1 -top-1 grid min-h-5 min-w-5 place-items-center rounded-full border-2 border-background bg-status-warning px-1 text-[10px] font-bold leading-none text-foreground"
        >
          {queuedCount}
        </span>
      )}
      <span className="sr-only">
        {serverStatus === "running" ? "Queue task" : "Send task"}
      </span>
    </Button>
    {modes.length > 1 && (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            size="icon"
            disabled={sendBlocked}
            className="h-10 w-8 rounded-l-none rounded-r-full border-l border-primary-foreground/20 shadow-sm"
            title="More ways to send"
          >
            <ChevronUp className="size-4" />
            <span className="sr-only">More ways to send</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" side="top" className="w-72">
          <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">While the agent works</DropdownMenuLabel>
          <DropdownMenuItem className="min-h-11 items-start" onSelect={() => onSubmit("queue")}>
            <ListPlus className="mt-0.5" />
            <span className="grid flex-1">
              <span>Queue</span>
              <span className="text-[11px] text-muted-foreground">Send once this turn ends</span>
            </span>
            <ShortcutHint keys={["Enter"]} />
          </DropdownMenuItem>
          {modes.includes("steer") && (
            <DropdownMenuItem className="min-h-11 items-start" onSelect={() => onSubmit("steer")}>
              <CornerDownRight className="mt-0.5" />
              <span className="grid flex-1">
                <span>Steer</span>
                <span className="text-[11px] text-muted-foreground">Add to the turn it is working on</span>
              </span>
              <ShortcutHint keys={[modKey, "Enter"]} />
            </DropdownMenuItem>
          )}
          {modes.includes("interrupt") && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem className="min-h-11 items-start" variant="destructive" onSelect={() => onSubmit("interrupt")}>
                <OctagonX className="mt-0.5" />
                <span className="grid flex-1">
                  <span>Interrupt &amp; send</span>
                  <span className="text-[11px] text-muted-foreground">Stop this turn, then send</span>
                </span>
                <ShortcutHint keys={[modKey, "⇧", "Enter"]} />
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    )}
   </div>
  );
}

function ShortcutHint({keys}: {keys: string[]}) {
  return (
    <span className="ml-auto hidden shrink-0 items-center gap-0.5 pt-0.5 sm:flex" aria-hidden="true">
      {keys.map((key) => (
        <Kbd key={key} small>{key}</Kbd>
      ))}
    </span>
  );
}
