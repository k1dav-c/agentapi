"use client";

import {useCallback, useEffect, useRef, useState} from "react";
import type {createChatAPI} from "@/lib/chat-api";
import {sameValue} from "@/lib/agent-status";
import {QueueView, type QueuedMessage} from "@/lib/queue";

// How often the queue is read while no queue event came: a server that
// sends none (older than queue_update) says nothing of a queued message it
// sent.
const QUEUE_POLL_MS = 60_000;

// useQueue keeps the queued messages as the server has them, and changes
// them. A server that sends queue_update events (sync clients) says every
// change, applied with the other events (applyQueue). The queue is read
// (GET /queue) on every connection; after the chat changed it, a status
// change while it holds messages, and every QUEUE_POLL_MS, only while the
// server sends no queue events.
export function useQueue(api: ReturnType<typeof createChatAPI>) {
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([]);
  const viewRef = useRef<QueueView | null>(null);
  viewRef.current ??= new QueueView();
  const lastEventAtRef = useRef(0);

  // The same queue keeps its object: the composer and the cards that
  // select it don't render again on every read.
  const show = useCallback(() => {
    const shown = viewRef.current!.shown;
    setQueuedMessages((previous) => (sameValue(previous, shown) ? previous : shown));
  }, []);

  const refreshQueue = useCallback(async () => {
    const view = viewRef.current!;
    const since = view.events;
    try {
      if (view.read(await api.getQueue(), since)) show();
    } catch {
      // The connection status handler reports connectivity failures.
    }
  }, [api, show]);

  // A queue_update.
  const applyQueue = useCallback((queue: QueuedMessage[]) => {
    viewRef.current!.event(queue);
    lastEventAtRef.current = Date.now();
    show();
  }, [show]);

  // Reads the queue after a change, unless the server says it.
  const queueChanged = useCallback(async () => {
    if (viewRef.current!.events === 0) await refreshQueue();
  }, [refreshQueue]);

  // The agent's status changed: the server sends a queued message as soon
  // as the agent is free, and one that sends no queue events doesn't say.
  const queueMayHaveSent = useCallback(() => {
    const view = viewRef.current!;
    if (view.events === 0 && view.shown.length > 0) void refreshQueue();
  }, [refreshQueue]);

  useEffect(() => {
    const timer = setInterval(() => {
      if (Date.now() - lastEventAtRef.current >= QUEUE_POLL_MS) void refreshQueue();
    }, QUEUE_POLL_MS);
    return () => clearInterval(timer);
  }, [refreshQueue]);

  const updateQueuedMessage = async (id: number, content: string) => {
    await api.updateQueuedMessage(id, content);
    await queueChanged();
  };

  const deleteQueuedMessage = async (id: number) => {
    await api.deleteQueuedMessage(id);
    await queueChanged();
  };

  const steerQueuedMessage = async (id: number) => {
    try {
      await api.steerQueuedMessage(id);
    } finally {
      await queueChanged();
    }
  };

  // The new order shows at once, also as queue events come, until the
  // server took it or refused it (409: the queue changed in the meantime).
  const reorderQueue = async (ids: number[]) => {
    const view = viewRef.current!;
    view.reorder(ids);
    show();
    let ok = false;
    try {
      await api.reorderQueue(ids);
      ok = true;
    } finally {
      view.reordered(ok);
      show();
      await queueChanged();
    }
  };

  return {queuedMessages, refreshQueue, applyQueue, queueChanged, queueMayHaveSent, updateQueuedMessage, deleteQueuedMessage, steerQueuedMessage, reorderQueue};
}
