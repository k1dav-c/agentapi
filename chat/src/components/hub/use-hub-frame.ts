"use client";

import {useEffect, useRef, useState} from "react";
import {frameMessage, onFrameMessage, type Framed, type FromFrame} from "@/lib/hub-frame";

// useHubFrame follows the chat in the hub's frame (lib/hub-frame.ts): the
// open instance's chat draws the bar above itself once it is ready
// (FrameBar), with its own actions in it; until then the hub's bar shows.
// A chat that loads again in the frame says it is ready again, and is told
// the instance again (Framed.ready). The frame's element carries the
// instance in data-instance.
export function useHubFrame({onBack, onCancelUpdate}: {onBack: () => void; onCancelUpdate: (id: string) => void}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const [framed, setFramed] = useState<Framed>();
  const latest = useRef({onBack, onCancelUpdate});
  useEffect(() => {
    latest.current = {onBack, onCancelUpdate};
  });
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const chat = frame.current;
      const instance = chat?.dataset.instance;
      if (!chat || !instance || event.source !== chat.contentWindow) return;
      const message = frameMessage<FromFrame>(event);
      if (!message) return;
      if (message.type === "ready") {
        setFramed((previous) => onFrameMessage(previous, message, instance).framed);
        return;
      }
      const {ask} = onFrameMessage(undefined, message, instance);
      if (ask === "back") latest.current.onBack();
      else if (ask === "cancel-update") latest.current.onCancelUpdate(instance);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);
  return {frame, framed};
}
