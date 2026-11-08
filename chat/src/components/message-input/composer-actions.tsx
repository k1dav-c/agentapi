"use client";

import {useEffect, useState} from "react";
import {LoaderCircle, Mic, MicOff, MoreHorizontal, Paperclip, SlidersHorizontal, Square} from "lucide-react";
import {Button} from "../ui/button";
import {DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger} from "../ui/dropdown-menu";
import {Tooltip, TooltipContent, TooltipTrigger} from "../ui/tooltip";
import {useChatActions, useChatSelector} from "../chat-provider";
import type {ServerStatus} from "@/lib/agent-status";
import {turnActive, type DispatchMode} from "@/lib/dispatch";
import {FavoritesMenu} from "./favorites-menu";
import {SendButtonGroup} from "./send-button";

// The composer's buttons: favorites, attach, ⋯ (voice; on a phone also
// attach, and the agent's settings), send, and stop while the agent works.
// On a phone they sit beside the message; on a wider screen in the row of
// the agent's settings and usage, where attach is a button of its own.
export function ComposerActions({
  layout,
  disabled,
  serverStatus,
  modes,
  sendBlocked,
  modKey,
  onSubmit,
  onAttach,
  onInsertFavorite,
  speech,
  settings,
}: {
  layout: "phone" | "desktop";
  disabled: boolean;
  serverStatus: ServerStatus;
  modes: DispatchMode[];
  sendBlocked: boolean;
  // ⌘ or Ctrl, for the shortcut hints.
  modKey: string;
  onSubmit: (mode: DispatchMode) => void;
  onAttach: () => void;
  onInsertFavorite: (text: string) => void;
  speech: {listening: boolean; supported: boolean; toggle: () => void};
  // The phone's menu item that shows or hides the settings row, when there
  // is one to show.
  settings?: {asked: boolean; toggle: () => void};
}) {
  const phone = layout === "phone";
  const queuedCount = useChatSelector((chat) => chat.queuedMessages.length);
  return (
    <div className={phone ? "flex shrink-0 items-center gap-1 py-1 pr-1" : "flex min-w-0 flex-row items-center gap-2"}>
      <FavoritesMenu onInsert={onInsertFavorite} />
      {!phone && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="size-10 rounded-full text-muted-foreground hover:text-foreground"
              onClick={(event) => {
                event.preventDefault();
                onAttach();
              }}
              disabled={disabled}
            >
              <Paperclip />
              <span className="sr-only">Attach files</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>Attach files</TooltipContent>
        </Tooltip>
      )}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            size="icon"
            variant={speech.listening ? "secondary" : "ghost"}
            className="size-10 shrink-0 rounded-full text-muted-foreground hover:text-foreground"
            title="More input options"
          >
            <MoreHorizontal />
            <span className="sr-only">More input options</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-48">
          {phone && (
            <DropdownMenuItem onSelect={onAttach} disabled={disabled} className="min-h-10">
              <Paperclip /> Attach files
            </DropdownMenuItem>
          )}
          <DropdownMenuItem onSelect={speech.toggle} disabled={disabled || !speech.supported} className="min-h-10">
            {speech.listening ? <MicOff /> : <Mic />}
            {speech.listening ? "Stop voice input" : "Start voice input"}
          </DropdownMenuItem>
          {phone && settings && (
            <DropdownMenuItem onSelect={settings.toggle} className="min-h-10">
              <SlidersHorizontal /> {settings.asked ? "Hide settings and usage" : "Settings and usage"}
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      {(serverStatus === "stable" || serverStatus === "running") && (
        <SendButtonGroup
          serverStatus={serverStatus}
          modes={modes}
          sendBlocked={sendBlocked}
          queuedCount={queuedCount}
          modKey={modKey}
          onSubmit={onSubmit}
        />
      )}
      <StopButton disabled={disabled} />
    </div>
  );
}

// Stops what the agent is doing, while it can (ChatState.canStop); it
// shows it stopping until the turn ends, or the stop failed.
function StopButton({disabled}: {disabled: boolean}) {
  const canStop = useChatSelector((chat) => chat.canStop);
  const active = useChatSelector((chat) => turnActive(chat.dispatch));
  const {stop} = useChatActions();
  const [stopping, setStopping] = useState(false);
  useEffect(() => {
    if (!active) setStopping(false);
  }, [active]);
  if (!canStop) return null;
  const handleStop = () => {
    if (stopping) return;
    setStopping(true);
    void stop().then((stopped) => {
      if (!stopped) setStopping(false);
    });
  };
  return (
    <Button
      size="icon"
      type="button"
      variant="destructive"
      className="size-10 rounded-full shadow-sm"
      disabled={disabled || stopping}
      onClick={handleStop}
      title={stopping ? "Stopping agent" : "Stop agent"}
    >
      {stopping ? <LoaderCircle className="animate-spin" /> : <Square />}
      <span className="sr-only">{stopping ? "Stopping" : "Stop"}</span>
    </Button>
  );
}
