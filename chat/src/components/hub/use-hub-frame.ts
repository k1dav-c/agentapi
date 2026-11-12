"use client";

import {useCallback, useEffect, useRef, useState} from "react";
import {messageFromFrame} from "@/lib/hub-frame";
import {frameReady, keptReadies} from "@/lib/hub-frames";
import {useLatest} from "@/lib/use-stable-callback";

// useHubFrame follows the chats in the hub's frames (lib/hub-frame.ts): the
// open instance's (current) and those the hub keeps loaded behind it
// (kept, lib/hub-frames.ts). Each frame's element is registered by its
// instance (register), and a message counts only from a registered frame's
// chat. Every chat draws the bar above itself once it is ready (FrameBar),
// with its own actions in it; until then the hub's bar shows. A chat that
// loads again in its frame says it is ready again, and is told the
// instance again: readies counts them by instance, for the kept frames,
// whichever shows. The rest asks for what a person did in the chat, so
// only the open one's count: the bar's back arrow (a phone's) asks to go
// back (onBack), and its × for the overview (onHome); the bar's switch asks
// to open another agent of the machine (onOpen), its update chip to cancel
// the update (onCancelUpdate), and the composer's menu of favorites to
// manage them (onManageFavorites).
export function useHubFrame({
  current,
  kept,
  onBack,
  onHome,
  onCancelUpdate,
  onOpen,
  onManageFavorites,
}: {
  current: string | undefined;
  kept: readonly string[];
  onBack: () => void;
  onHome: () => void;
  onCancelUpdate: (id: string) => void;
  onOpen: (id: string) => void;
  onManageFavorites: () => void;
}) {
  const frames = useRef(new Map<string, HTMLIFrameElement>());
  // The readies of the frames kept when they were counted: a frame that
  // goes loses its count at once, so one opened again shows the hub's bar
  // until its new chat is ready.
  const [counted, setCounted] = useState<{kept: readonly string[]; readies: ReadonlyMap<string, number>}>({kept, readies: new Map()});
  if (counted.kept !== kept) setCounted({kept, readies: keptReadies(counted.readies, kept)});
  const latest = useLatest({current, onBack, onHome, onCancelUpdate, onOpen, onManageFavorites});
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      for (const [instance, chat] of frames.current) {
        const message = messageFromFrame(event, chat.contentWindow);
        if (!message) continue;
        if (message.type === "ready") {
          setCounted((previous) => ({...previous, readies: frameReady(previous.readies, instance, previous.kept)}));
          return;
        }
        // A chat out of sight is inert: nothing a person did there.
        if (instance !== latest.current.current) return;
        switch (message.type) {
          case "back":
            latest.current.onBack();
            break;
          case "home":
            latest.current.onHome();
            break;
          case "cancel-update":
            latest.current.onCancelUpdate(instance);
            break;
          case "manage-favorites":
            latest.current.onManageFavorites();
            break;
          case "open":
            latest.current.onOpen(message.id);
            break;
        }
        return;
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [latest]);
  // register is a frame's ref: its element while it is in the page.
  const register = useCallback((instance: string, element: HTMLIFrameElement | null) => {
    if (element) frames.current.set(instance, element);
    else frames.current.delete(instance);
  }, []);
  return {register, readies: keptReadies(counted.readies, kept)};
}
