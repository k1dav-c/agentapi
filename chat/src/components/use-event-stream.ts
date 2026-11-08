"use client";

import {startTransition, useCallback, useEffect, useRef, useState, type Dispatch, type RefObject} from "react";
import {toast} from "sonner";
import {ChatStream} from "@/lib/chat-stream";
import {initialConnectionState, type ConnectionState} from "@/lib/event-source";
import {loadSession} from "@/lib/session-cache";
import {bootstrapSession, ensureSession, reconnectWhenSignedIn, withCredentials} from "@/lib/auth";
import {isLoginNotice} from "@/lib/login";
import type {QueuedMessage} from "@/lib/queue";
import type {SyncTracker} from "@/lib/session-sync";
import {applyStatusChange, update, type AgentStatus} from "@/lib/agent-status";
import type {ConversationAction} from "@/lib/conversation-state";
import type {EventBatch} from "@/lib/pending-events";
import {agentErrorKey, dismissedErrors, rememberDismissed} from "@/lib/agent-errors";

// How long to wait for the cache before connecting without it.
const CACHE_LOAD_TIMEOUT_MS = 400;

// useEventStream keeps the chat in step with the server's /events. It
// shows the cached transcript first and connects from where the cache left
// off; the stream (ChatStream) hands on the events in batches, a replay at
// once when it is complete, and keeps the connection open. It changes the
// conversation through dispatch (lib/conversation-state), and returns the
// connection's state.
export function useEventStream({
  agentAPIUrl,
  syncRef,
  dispatch,
  updateAgentStatus,
  agentStatusRef,
  refreshQueue,
  applyQueue,
  queueMayHaveSent,
  onReplayed,
}: {
  agentAPIUrl: string;
  syncRef: RefObject<SyncTracker>;
  dispatch: Dispatch<ConversationAction>;
  updateAgentStatus: (change: (status: AgentStatus) => AgentStatus) => void;
  agentStatusRef: RefObject<AgentStatus>;
  // Reading the queue (GET /queue), as every connection does; applying a
  // queue_update; and telling of a status change, after which the server
  // may have sent a queued message.
  refreshQueue: () => Promise<void>;
  applyQueue: (queue: QueuedMessage[]) => void;
  queueMayHaveSent: () => void;
  // Called once the replay of what changed since the cache or the last
  // connection was applied: it doesn't tell of what the server dropped.
  onReplayed: () => void;
}) {
  const [connection, setConnection] = useState<ConnectionState>(initialConnectionState);
  // Errors the server replays on every connection that this page showed
  // already.
  const shownErrorsRef = useRef(new Set<string>());
  const streamRef = useRef<ChatStream | null>(null);
  const cacheLoadedForRef = useRef("");

  const apply = useCallback((batch: EventBatch) => {
    // The transcript renders in a transition: a long one takes a while,
    // and typing or a click shouldn't wait for it. Requests apply at once
    // (see request_update), so they are never behind the point.
    startTransition(() => dispatch({type: "batch", batch}));
    if (batch.replaceHeld || batch.held || batch.requests.length > 0) dispatch({type: "batchRequests", batch});
    if (batch.queue) applyQueue(batch.queue);
  }, [applyQueue, dispatch]);

  // Reconnect now, as the user asked. Once the user closed the sign-in
  // dialog, reconnects in the background stop asking for the token; this
  // asks again, and reconnects once signed in.
  const reconnectByUser = useCallback(() => {
    streamRef.current?.reconnectNow();
    void ensureSession(agentAPIUrl).then((signedIn) => {
      if (signedIn) streamRef.current?.reconnectNow();
    });
  }, [agentAPIUrl]);

  useEffect(() => {
    let disposed = false;
    if (!agentAPIUrl) {
      console.warn("agentAPIUrl is not set, SSE connection cannot be established.");
      updateAgentStatus((status) => update(status, {serverStatus: "offline"}));
      return;
    }
    const stream = new ChatStream(agentAPIUrl, syncRef.current, {
      apply,
      replayed: onReplayed,
      statusChange: (data) => {
        updateAgentStatus((status) => applyStatusChange(status, data));
        queueMayHaveSent();
      },
      agentError: (data) => {
        const key = agentErrorKey(data);
        // Shown on this page already, or closed before a reload.
        if (shownErrorsRef.current.has(key) || dismissedErrors(agentAPIUrl).has(key)) return;
        shownErrorsRef.current.add(key);
        // The login card says it while the login is needed; afterwards
        // the notice is stale.
        if (isLoginNotice(data.message, agentStatusRef.current.loginRequired)) return;
        // A toast that stays until it is closed; closed, it stays closed
        // after a reload.
        const options = {duration: Infinity, onDismiss: () => rememberDismissed(agentAPIUrl, key)};
        if (data.level === "error") toast.error(data.message, options);
        else if (data.level === "warning") toast.warning(data.message, options);
        else toast.info(data.message, options);
      },
      opened: () => void refreshQueue(),
      // A 401 looks like any other failure: see reconnectWhenSignedIn.
      failed: () => {
        void reconnectWhenSignedIn(agentAPIUrl, () => {
          if (!disposed) stream.reconnectNow();
        });
      },
      connection: setConnection,
    }, {withCredentials: withCredentials(agentAPIUrl)});
    streamRef.current = stream;

    // Show the cached transcript right away and connect from where it
    // left off. Without a cache (or if it is slow to read) connect anyway.
    let started = false;
    const start = () => {
      if (started || disposed) return;
      started = true;
      // Sign in first (a `#token=` link, or the dialog when the server
      // needs a token). Only the first connection waits for it.
      void bootstrapSession(agentAPIUrl).finally(() => {
        if (!disposed) stream.start();
      });
    };
    if (cacheLoadedForRef.current !== agentAPIUrl) {
      cacheLoadedForRef.current = agentAPIUrl;
      void loadSession(agentAPIUrl).then((cached) => {
        if (cached && !started && !disposed) {
          syncRef.current.restore(cached.epoch, cached.seq);
          dispatch({type: "restore", cached});
        }
        start();
      });
      setTimeout(start, CACHE_LOAD_TIMEOUT_MS);
    } else {
      start();
    }

    return () => {
      disposed = true;
      stream.stop();
      streamRef.current = null;
    };
  }, [agentAPIUrl, agentStatusRef, apply, dispatch, onReplayed, queueMayHaveSent, refreshQueue, syncRef, updateAgentStatus]);

  return {
    connectionStatus: connection.status,
    reconnectAttempt: connection.attempt,
    nextReconnectAt: connection.nextAttemptAt,
    reconnectByUser,
  };
}
