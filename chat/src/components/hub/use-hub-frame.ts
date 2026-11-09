"use client";

import {useEffect, useRef, useState} from "react";
import {messageFromFrame, readyAgain, type Framed} from "@/lib/hub-frame";
import {useLatest} from "@/lib/use-stable-callback";

// useHubFrame follows the chat in the hub's frame (lib/hub-frame.ts): the
// open instance's chat draws the bar above itself once it is ready
// (FrameBar), with its own actions in it; until then the hub's bar shows.
// A chat that loads again in the frame says it is ready again, and is told
// the instance again (Framed.ready). The frame's element carries the
// instance in data-instance. The bar's switch asks to open another agent of
// the machine (onOpen), and the composer's menu of favorites to manage them
// (onManageFavorites).
export function useHubFrame({
  onBack,
  onCancelUpdate,
  onOpen,
  onManageFavorites,
}: {
  onBack: () => void;
  onCancelUpdate: (id: string) => void;
  onOpen: (id: string) => void;
  onManageFavorites: () => void;
}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const [framed, setFramed] = useState<Framed>();
  const latest = useLatest({onBack, onCancelUpdate, onOpen, onManageFavorites});
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const chat = frame.current;
      const instance = chat?.dataset.instance;
      const message = instance ? messageFromFrame(event, chat?.contentWindow) : undefined;
      if (!instance || !message) return;
      switch (message.type) {
        case "ready":
          setFramed((previous) => readyAgain(previous, instance));
          break;
        case "back":
          latest.current.onBack();
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
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [latest]);
  return {frame, framed};
}
