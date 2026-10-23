"use client";

import {useEffect, useLayoutEffect, useRef} from "react";
import {saveSession} from "@/lib/session-cache";
import type {SyncPoint} from "@/lib/pending-events";
import type {Transport} from "@/lib/dispatch";
import type {NativeRequest} from "@/lib/native-requests";
import type {ServerTask} from "@/lib/server-tasks";
import {isDraftMessage, type DraftMessage, type Message, type RichMessage} from "@/lib/conversation";

// Minimum time between writes of the conversation cache.
const CACHE_SAVE_INTERVAL_MS = 2000;

// useSessionCache keeps the conversation cache current (lib/session-cache),
// at most every CACHE_SAVE_INTERVAL_MS, and once more when the page is
// hidden: a reload shows the transcript at once and connects from where
// it left off. A save writes only the messages that changed.
export function useSessionCache({
  agentAPIUrl,
  synced,
  messages,
  richMessages,
  requests,
  tasks,
  transport,
}: {
  agentAPIUrl: string;
  // The point of the conversation the state is at (see useEventStream).
  synced: SyncPoint;
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  requests: NativeRequest[];
  tasks: ServerTask[];
  transport: Transport;
}) {
  const saveRequestRef = useRef<() => void>(() => {});
  // The state last rendered, and its point: what is saved is a state and
  // the changes it holds, so that a reload resumes from there.
  const latestRef = useRef({synced, messages, richMessages, requests, tasks, transport});
  useLayoutEffect(() => {
    latestRef.current = {synced, messages, richMessages, requests, tasks, transport};
  });
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    let lastSave = 0;
    const save = () => {
      timer = null;
      // Nothing to save until the state is at a point of a conversation;
      // the first one is saved as soon as it is rendered.
      const {epoch, seq} = latestRef.current.synced;
      if (!epoch) return;
      lastSave = Date.now();
      void saveSession(agentAPIUrl, {
        epoch,
        seq,
        messages: latestRef.current.messages.filter((message): message is Message => !isDraftMessage(message)),
        richMessages: latestRef.current.richMessages,
        requests: latestRef.current.requests,
        tasks: latestRef.current.tasks,
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
  }, [agentAPIUrl]);
  useEffect(() => {
    saveRequestRef.current();
  }, [synced, messages, richMessages, requests, tasks]);
}
