"use client";

import {startTransition, useCallback, useEffect, useRef, useState, type Dispatch, type RefObject, type SetStateAction} from "react";
import {toast} from "sonner";
import {ChatStream} from "@/lib/chat-stream";
import {initialConnectionState, type ConnectionState} from "@/lib/event-source";
import {loadSession, mergeByKey} from "@/lib/session-cache";
import {bootstrapSession, ensureSession, withCredentials} from "@/lib/auth";
import type {createChatAPI} from "@/lib/chat-api";
import {parseTransport, type Transport} from "@/lib/dispatch";
import {isLoginNotice} from "@/lib/login";
import {mergeRequests, type NativeRequest} from "@/lib/native-requests";
import {mergeTasks, type ServerTask} from "@/lib/server-tasks";
import type {QueuedMessage} from "@/lib/queue";
import type {SyncTracker} from "@/lib/session-sync";
import {applyStatusChange, update, type AgentStatus} from "@/lib/agent-status";
import {isDraftMessage, richMessageKey, withoutSeq, type DraftMessage, type Message, type RichMessage} from "@/lib/conversation";
import type {EventBatch, SyncPoint} from "@/lib/pending-events";
import {agentErrorKey, dismissedErrors, rememberDismissed} from "@/lib/agent-errors";

// How long to wait for the cache before connecting without it.
const CACHE_LOAD_TIMEOUT_MS = 400;

// useEventStream keeps the chat in step with the server's /events. It
// shows the cached transcript first and connects from where the cache left
// off; the stream (ChatStream) hands on the events in batches, a replay at
// once when it is complete, and keeps the connection open. It returns,
// besides the connection's state, the point of the conversation the
// rendered state is at (synced), for the cache.
export function useEventStream({
  agentAPIUrl,
  api,
  syncRef,
  setMessages,
  setRichMessages,
  setRequests,
  setTasks,
  setTransport,
  updateAgentStatus,
  agentStatusRef,
  refreshQueue,
  applyQueue,
  queueMayHaveSent,
  onReplayed,
}: {
  agentAPIUrl: string;
  api: ReturnType<typeof createChatAPI>;
  syncRef: RefObject<SyncTracker>;
  setMessages: Dispatch<SetStateAction<(Message | DraftMessage)[]>>;
  setRichMessages: Dispatch<SetStateAction<RichMessage[]>>;
  setRequests: Dispatch<SetStateAction<NativeRequest[]>>;
  setTasks: Dispatch<SetStateAction<ServerTask[]>>;
  setTransport: Dispatch<SetStateAction<Transport>>;
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
  // The point of the conversation the rendered state is at. It is set with
  // the updates that bring the state there, and rendered with them: the
  // tracker's watermark runs ahead, and a cache that saved it with the
  // state of the last render could claim changes the state didn't hold,
  // which ?since= never sends again.
  const [synced, setSynced] = useState<SyncPoint>({epoch: "", seq: 0});
  const cacheLoadedForRef = useRef("");

  // Sets synced to where the tracker is, for a state set at the same time.
  const markSynced = useCallback(() => {
    const {epoch, watermark: seq} = syncRef.current;
    setSynced((previous) => (previous.epoch === epoch && previous.seq === seq ? previous : {epoch, seq}));
  }, [syncRef]);

  const apply = useCallback((batch: EventBatch) => {
    const {replace, point} = batch;
    const messageUpdates = batch.messages;
    const richUpdates = batch.richMessages;
    const requestUpdates = batch.requests;
    const taskUpdates = batch.tasks;

    // The transcript renders in a transition: a long one takes a while,
    // and typing or a click shouldn't wait for it. Requests apply at once
    // (see request_update), so they are never behind the point.
    startTransition(() => {
      setSynced((previous) => (previous.epoch === point.epoch && previous.seq === point.seq ? previous : point));
      if (replace || messageUpdates.length > 0) {
        setMessages((previous) => {
          // A server message supersedes drafts that are still being sent;
          // failed drafts stay until they are retried or dismissed.
          const kept = previous.filter(
            (message) =>
              (replace ? isDraftMessage(message) : true) &&
              (!isDraftMessage(message) || message.deliveryStatus === "failed"),
          );
          return mergeByKey(
            kept,
            messageUpdates.map(withoutSeq),
            (message) => (isDraftMessage(message) ? undefined : String(message.id)),
          );
        });
      }
      if (replace || richUpdates.length > 0) {
        setRichMessages((previous) =>
          mergeByKey(replace ? [] : previous, richUpdates.map(withoutSeq), richMessageKey),
        );
      }
      // Tasks keep their seq, as requests do, so an update never replaces
      // a later one.
      if (replace || taskUpdates.length > 0) setTasks((previous) => mergeTasks(replace ? [] : previous, taskUpdates));
    });
    if (replace || requestUpdates.length > 0) {
      // Requests keep their seq: it decides between an event and a state
      // read from the REST API (see mergeRequests).
      setRequests((previous) => mergeRequests(replace ? [] : previous, requestUpdates));
    }
    if (batch.queue) applyQueue(batch.queue);
  }, [applyQueue, setMessages, setRequests, setRichMessages, setTasks]);

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
      transport: setTransport,
      askTransport: () => {
        void api.getStatus().then((status) => {
          if (!disposed) setTransport(status.transport);
        }).catch(() => {
          // Reported through the connection status.
        });
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
      // EventSource doesn't expose the status code; a 401 (no session
      // cookie, or the token changed) shows up as an error like any other.
      // Ask the server, and reconnect right away once signed in. Nobody
      // asked for this reconnect, so a closed dialog stays closed.
      failed: () => {
        void ensureSession(agentAPIUrl, {background: true}).then((signedIn) => {
          if (signedIn && !disposed) stream.reconnectNow();
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
          markSynced();
          setMessages((previous) => [...cached.messages, ...previous.filter(isDraftMessage)]);
          setRichMessages(cached.richMessages);
          setRequests(cached.requests);
          setTasks(cached.tasks);
          const cachedTransport = parseTransport(cached.transport);
          if (cachedTransport) setTransport(cachedTransport);
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
  }, [agentAPIUrl, agentStatusRef, api, apply, markSynced, onReplayed, queueMayHaveSent, refreshQueue, setMessages, setRequests, setRichMessages, setTasks, setTransport, syncRef, updateAgentStatus]);

  return {
    connectionStatus: connection.status,
    reconnectAttempt: connection.attempt,
    nextReconnectAt: connection.nextAttemptAt,
    reconnectByUser,
    synced,
    markSynced,
  };
}
