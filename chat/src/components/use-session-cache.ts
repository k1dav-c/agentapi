"use client";

import {useEffect, useRef, type RefObject} from "react";
import {saveSession} from "@/lib/session-cache";
import type {SyncTracker} from "@/lib/session-sync";
import type {Transport} from "@/lib/dispatch";
import type {NativeRequest} from "@/lib/native-requests";
import {isDraftMessage, type DraftMessage, type Message, type RichMessage} from "@/lib/conversation";

// Minimum time between writes of the conversation cache.
const CACHE_SAVE_INTERVAL_MS = 2000;

// useSessionCache keeps the conversation cache current (lib/session-cache),
// at most every CACHE_SAVE_INTERVAL_MS, and once more when the page is
// hidden: a reload shows the transcript at once and connects from where
// it left off. A save writes only the messages that changed.
export function useSessionCache({
  agentAPIUrl,
  syncRef,
  messages,
  richMessages,
  requests,
  transport,
}: {
  agentAPIUrl: string;
  syncRef: RefObject<SyncTracker>;
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  requests: NativeRequest[];
  transport: Transport;
}) {
  const saveRequestRef = useRef<() => void>(() => {});
  const latestRef = useRef({messages, richMessages, requests, transport});
  latestRef.current = {messages, richMessages, requests, transport};
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    let lastSave = 0;
    const save = () => {
      timer = null;
      lastSave = Date.now();
      // The watermark doesn't pass a replay still on its way, so a reload
      // gets the rest of it again.
      const {epoch, watermark: seq} = syncRef.current;
      if (!epoch) return;
      void saveSession(agentAPIUrl, {
        epoch,
        seq,
        messages: latestRef.current.messages.filter((message): message is Message => !isDraftMessage(message)),
        richMessages: latestRef.current.richMessages,
        requests: latestRef.current.requests,
        transport: latestRef.current.transport,
      });
    };
    const onHide = () => {
      if (document.visibilityState === "hidden") save();
    };
    document.addEventListener("visibilitychange", onHide);
    window.addEventListener("pagehide", save);
    saveRequestRef.current = () => {
      if (timer) return;
      timer = setTimeout(save, Math.max(0, lastSave + CACHE_SAVE_INTERVAL_MS - Date.now()));
    };
    return () => {
      document.removeEventListener("visibilitychange", onHide);
      window.removeEventListener("pagehide", save);
      if (timer) clearTimeout(timer);
      saveRequestRef.current = () => {};
    };
  }, [agentAPIUrl, syncRef]);
  useEffect(() => {
    saveRequestRef.current();
  }, [messages, richMessages, requests]);
}
