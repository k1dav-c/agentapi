"use client";

import {useCallback, useRef, useState} from "react";
import type {QueuedMessage} from "./chat-provider";
import type {createChatAPI} from "@/lib/chat-api";
import {sameValue} from "@/lib/agent-status";

// useQueue keeps the queued messages as the server has them (GET /queue),
// and changes them.
export function useQueue(api: ReturnType<typeof createChatAPI>) {
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([]);
  const lastQueueRefreshRef = useRef(0);
  const queuedCountRef = useRef(0);
  const refreshQueue = useCallback(async (force = false) => {
    const now = Date.now();
    // Throttle: skip if last refresh was less than 30s ago, unless forced.
    // A non-empty queue is never throttled: the server sends a queued task
    // as soon as the agent is free, and a stale entry looks like a message
    // that failed to send.
    if (!force && queuedCountRef.current === 0 && now - lastQueueRefreshRef.current < 30_000) return;
    lastQueueRefreshRef.current = now;
    try {
      const queue = await api.getQueue();
      queuedCountRef.current = queue.length;
      // The same queue keeps its object: the composer and the cards that
      // select it don't render again on every refresh.
      setQueuedMessages((previous) => (sameValue(previous, queue) ? previous : queue));
    } catch {
      // The connection status handler reports connectivity failures.
    }
  }, [api]);

  const updateQueuedMessage = async (id: number, content: string) => {
    await api.updateQueuedMessage(id, content);
    await refreshQueue(true);
  };

  const deleteQueuedMessage = async (id: number) => {
    await api.deleteQueuedMessage(id);
    await refreshQueue(true);
  };

  const steerQueuedMessage = async (id: number) => {
    try {
      await api.steerQueuedMessage(id);
    } finally {
      await refreshQueue(true);
    }
  };

  const reorderQueue = async (ids: number[]) => {
    // Show the new order at once; the refresh corrects it if the server
    // refused.
    setQueuedMessages((previous) =>
      ids.flatMap((id) => previous.filter((message) => message.id === id)),
    );
    try {
      await api.reorderQueue(ids);
    } finally {
      await refreshQueue(true);
    }
  };

  return {queuedMessages, refreshQueue, updateQueuedMessage, deleteQueuedMessage, steerQueuedMessage, reorderQueue};
}
