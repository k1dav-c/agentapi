"use client";

import {startTransition, useCallback, useEffect, useRef, useState, type Dispatch, type RefObject, type SetStateAction} from "react";
import {toast} from "sonner";
import {getReconnectDelay, reconnectAtOnce} from "@/lib/reconnect";
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
import {PendingEvents, type SyncPoint} from "@/lib/pending-events";
import {
  messageFromEvent,
  requestFromEvent,
  queueFromEvent,
  richMessageFromEvent,
  taskFromEvent,
  type AgentErrorEvent,
  type MessageUpdateEvent,
  type QueueUpdateEvent,
  type RequestUpdateEvent,
  type RichMessageUpdateEvent,
  type SessionSyncEvent,
  type StatusChangeEvent,
  type TaskUpdateEvent,
} from "@/lib/events";
import type {ConnectionStatus} from "./chat-provider";
import {agentErrorKey, dismissedErrors, rememberDismissed} from "@/lib/agent-errors";

// How long incoming conversation events are collected before they are
// applied together. A reload replays the whole session as one event per
// message; applying them one by one re-rendered the transcript each time.
const EVENT_FLUSH_MS = 32;
// How long to wait for the cache before connecting without it.
const CACHE_LOAD_TIMEOUT_MS = 400;
// While a reconnect replays state, updates are held until the replay is
// complete and applied at once; a very long replay still shows progress
// this often.
const REPLAY_FLUSH_MAX_MS = 1500;

// The server sends a heartbeat event every 15s. If nothing (heartbeat or
// otherwise) arrives for this long, the connection is considered dead even
// when the EventSource still reports itself as open — which happens when
// the TCP connection dies without a FIN, e.g. after system sleep.
const STALE_CONNECTION_MS = 45_000;
// How long a reconnect that doesn't show (see reconnectAtOnce) may take
// before it shows that the chat is reconnecting.
const QUIET_RECONNECT_MS = 3000;

// useEventStream keeps the chat in step with the server's /events. It
// shows the cached transcript first and connects from where the cache left
// off; applies the events in batches (EVENT_FLUSH_MS), a replay at once
// when it is complete (SyncTracker); and reconnects when the connection
// drops, goes stale or the browser comes back online: at once when the
// server closed a stream that was open, else with backoff. It returns,
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
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>("reconnecting");
  // Errors the server replays on every connection that this page showed
  // already.
  const shownErrorsRef = useRef(new Set<string>());
  const eventSourceRef = useRef<EventSource | null>(null);
  const lastEventAtRef = useRef(Date.now());
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef(0);
  // When the latest reconnects that didn't show were; see reconnectAtOnce.
  const quickReconnectsRef = useRef<number[]>([]);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  const [nextReconnectAt, setNextReconnectAt] = useState<number | null>(null);
  const [reconnectNonce, setReconnectNonce] = useState(0);
  // Events received but not applied yet; see EVENT_FLUSH_MS.
  const pendingRef = useRef<PendingEvents | null>(null);
  pendingRef.current ??= new PendingEvents(syncRef.current);
  // The point of the conversation the rendered state is at. It is set with
  // the updates that bring the state there, and rendered with them: the
  // tracker's watermark runs ahead, and a cache that saved it with the
  // state of the last render could claim changes the state didn't hold,
  // which ?since= never sends again.
  const [synced, setSynced] = useState<SyncPoint>({epoch: "", seq: 0});
  // The replay of a connection that resumed (not a full one) is on its way,
  // and was applied, for onReplayed.
  const partialReplayRef = useRef(false);
  const replayedRef = useRef(false);
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cacheLoadedForRef = useRef("");

  // Sets synced to where the tracker is, for a state set at the same time.
  const markSynced = useCallback(() => {
    const {epoch, watermark: seq} = syncRef.current;
    setSynced((previous) => (previous.epoch === epoch && previous.seq === seq ? previous : {epoch, seq}));
  }, [syncRef]);

  const flushEvents = useCallback(() => {
    flushTimerRef.current = null;
    const batch = pendingRef.current!.take();
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
    if (replayedRef.current) {
      replayedRef.current = false;
      onReplayed();
    }
  }, [applyQueue, onReplayed, setMessages, setRequests, setRichMessages, setTasks]);

  // Schedules applying the events received so far. An event that completes
  // the replay (see SyncTracker) applies the replay now, in one render.
  const scheduleFlush = useCallback((event?: MessageEvent, immediate = false) => {
    if (event && syncRef.current.noteEvent(event.lastEventId)) {
      replayedRef.current ||= partialReplayRef.current;
      partialReplayRef.current = false;
      if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
      flushTimerRef.current = setTimeout(flushEvents, 0);
      return;
    }
    const replaying = syncRef.current.replaying;
    if (immediate && !replaying) {
      if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
      flushEvents();
      return;
    }
    if (flushTimerRef.current) return;
    flushTimerRef.current = setTimeout(flushEvents, replaying ? REPLAY_FLUSH_MAX_MS : EVENT_FLUSH_MS);
  }, [flushEvents, syncRef]);

  const reconnectNow = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    eventSourceRef.current?.close();
    reconnectAttemptRef.current = 0;
    setReconnectAttempt(0);
    setNextReconnectAt(null);
    setConnectionStatus("reconnecting");
    setReconnectNonce((value) => value + 1);
  }, []);
  // Reconnect now, as the user asked. Once the user closed the sign-in
  // dialog, reconnects in the background stop asking for the token; this
  // asks again, and reconnects once signed in.
  const reconnectByUser = useCallback(() => {
    reconnectNow();
    void ensureSession(agentAPIUrl).then((signedIn) => {
      if (signedIn) reconnectNow();
    });
  }, [agentAPIUrl, reconnectNow]);
  // Set up SSE connection to the events endpoint
  useEffect(() => {
    let disposed = false;

    const handleOffline = () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
        reconnectTimeoutRef.current = null;
      }
      eventSourceRef.current?.close();
      setNextReconnectAt(null);
      setConnectionStatus("offline");
    };
    const handleOnline = () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
        reconnectTimeoutRef.current = null;
      }
      reconnectAttemptRef.current = 0;
      setReconnectAttempt(0);
      setNextReconnectAt(null);
      setConnectionStatus("reconnecting");
      setupEventSource();
    };
    window.addEventListener("offline", handleOffline);
    window.addEventListener("online", handleOnline);

    // Reconnect promptly when the tab becomes visible again. Browsers
    // throttle timers in background tabs, so a scheduled reconnect may not
    // have fired yet, and a connection that died without a FIN (e.g. after
    // system sleep) never fires onerror at all. Server heartbeats let us
    // detect the silent case: no event for STALE_CONNECTION_MS means the
    // connection is dead even if the EventSource still reports itself open.
    const handleVisible = () => {
      if (document.visibilityState !== "visible") return;
      const eventSource = eventSourceRef.current;
      const stale =
        Date.now() - lastEventAtRef.current > STALE_CONNECTION_MS;
      if (
        !eventSource ||
        eventSource.readyState === EventSource.CLOSED ||
        stale
      ) {
        reconnectNow();
      }
    };
    document.addEventListener("visibilitychange", handleVisible);
    window.addEventListener("focus", handleVisible);

    // Function to create and set up EventSource. With quietFor, the
    // connection status stays as it is for that long (see onerror).
    const setupEventSource = (quietFor?: number) => {
      if (disposed) return null;

      if (eventSourceRef.current) {
        eventSourceRef.current.close();
      }

      if (!agentAPIUrl) {
        console.warn(
          "agentAPIUrl is not set, SSE connection cannot be established."
        );
        updateAgentStatus((status) => update(status, {serverStatus: "offline"}));
        return null; // Don't try to connect if URL is empty
      }

      const {epoch, watermark} = syncRef.current;
      const eventSource = new EventSource(
        epoch
          ? `${agentAPIUrl}/events?sync=1&since=${watermark}&epoch=${encodeURIComponent(epoch)}`
          : `${agentAPIUrl}/events?sync=1`,
        {withCredentials: withCredentials(agentAPIUrl)},
      );
      eventSourceRef.current = eventSource;
      const quietTimer = quietFor === undefined ? undefined : setTimeout(() => {
        if (!disposed && eventSourceRef.current === eventSource) setConnectionStatus("reconnecting");
      }, quietFor);
      // Servers whose status_change doesn't carry the transport report it in
      // GET /status, asked once per connection.
      let askedTransport = false;
      // Servers that predate session_sync replay everything on every
      // connection without saying so; treat that replay as the full state.
      let synced = false;
      const assumeFullReplay = () => {
        if (synced) return;
        synced = true;
        syncRef.current.reset();
        pendingRef.current!.replaceAll();
      };

      eventSource.addEventListener("session_sync", (event) => {
        lastEventAtRef.current = Date.now();
        synced = true;
        const data = JSON.parse(event.data) as SessionSyncEvent;
        const full = syncRef.current.begin(data, event.lastEventId);
        // Everything that follows is the full state: it replaces what the
        // cache or an earlier connection left. Otherwise it is what changed
        // since, which says nothing of the closed requests the server let
        // go of in the meantime: once it is applied, onReplayed.
        if (full) pendingRef.current!.replaceAll();
        partialReplayRef.current = !full && syncRef.current.replaying;
        replayedRef.current = !full && !syncRef.current.replaying;
        if (full || !syncRef.current.replaying) scheduleFlush();
      });

      // Server-sent keep-alive; only used to detect dead connections.
      eventSource.addEventListener("heartbeat", (event) => {
        lastEventAtRef.current = Date.now();
        if (syncRef.current.replaying) scheduleFlush(event);
      });

      // Handle message updates
      eventSource.addEventListener("message_update", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        pendingRef.current!.addMessage(messageFromEvent(JSON.parse(event.data) as MessageUpdateEvent));
        scheduleFlush(event);
      });

      eventSource.addEventListener("rich_message_update", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        pendingRef.current!.addRichMessage(richMessageFromEvent(JSON.parse(event.data) as RichMessageUpdateEvent));
        scheduleFlush(event);
      });

      // A request the agent waits on (native transport) opened or changed.
      // Requests are part of the replay, like messages. Outside a replay one
      // applies at once: the status_change the server sends right after it
      // (now "stable", waiting) must not show before the request does.
      eventSource.addEventListener("request_update", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        pendingRef.current!.addRequest(requestFromEvent(JSON.parse(event.data) as RequestUpdateEvent));
        scheduleFlush(event, true);
      });

      // A task the server derives started or changed (sync clients). Tasks
      // are part of the replay, like requests, and come last in it.
      eventSource.addEventListener("task_update", (event) => {
        lastEventAtRef.current = Date.now();
        pendingRef.current!.addTask(taskFromEvent(JSON.parse(event.data) as TaskUpdateEvent));
        scheduleFlush(event);
      });

      // The message queue changed (sync clients): the whole queue, applied
      // with the messages it sent. The replay holds the latest.
      eventSource.addEventListener("queue_update", (event) => {
        lastEventAtRef.current = Date.now();
        const data = JSON.parse(event.data) as QueueUpdateEvent;
        pendingRef.current!.setQueue(queueFromEvent(data), data.seq);
        scheduleFlush(event);
      });

      // Handle status changes
      eventSource.addEventListener("status_change", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        const data = JSON.parse(event.data) as StatusChangeEvent;
        // The replay's last event may be its status.
        if (syncRef.current.replaying) scheduleFlush(event);
        updateAgentStatus((status) => applyStatusChange(status, data));
        queueMayHaveSent();
        // The transport decides whether the terminal (TTY mode, raw keys)
        // exists at all.
        const reported = parseTransport(data.transport);
        if (reported) {
          setTransport(reported);
        } else if (!askedTransport) {
          askedTransport = true;
          void api.getStatus().then((status) => {
            if (!disposed) setTransport(status.transport);
          }).catch(() => {
            // Reported through the connection status.
          });
        }
      });

      // Handle agent error events
      eventSource.addEventListener("agent_error", (event) => {
        const messageEvent = event as MessageEvent;
        if (syncRef.current.replaying) scheduleFlush(messageEvent);
        try {
          const data = JSON.parse(messageEvent.data) as AgentErrorEvent;
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
          if (data.level === "error") {
            toast.error(data.message, options);
          } else if (data.level === "warning") {
            toast.warning(data.message, options);
          } else {
            toast.info(data.message, options);
          }
        } catch (e) {
          console.error("Failed to parse agent_error event data:", e);
        }
      });

      // Handle connection open (server is online)
      let opened = false;
      eventSource.onopen = () => {
        opened = true;
        if (quietTimer) clearTimeout(quietTimer);
        lastEventAtRef.current = Date.now();
        reconnectAttemptRef.current = 0;
        setReconnectAttempt(0);
        setNextReconnectAt(null);
        setConnectionStatus("connected");
        void refreshQueue();
        // Connection is established, but we'll wait for status_change event
        // for the actual server status
        console.log("EventSource connection established - messages reset");
      };

      // Handle connection errors
      eventSource.onerror = (error) => {
        eventSource.close();
        if (quietTimer) clearTimeout(quietTimer);
        // A stream that was open ended: the server closed it (it closes
        // them all on a resync) or the network dropped it. Reconnecting
        // at once, from where the chat is, shows nothing; if that doesn't
        // connect soon, or fails, the usual reconnecting shows.
        if (navigator.onLine && reconnectAtOnce(opened, quickReconnectsRef.current)) {
          console.info("The event stream ended; reconnecting");
          setupEventSource(QUIET_RECONNECT_MS);
          return;
        }
        console.error("EventSource error:", error);
        setConnectionStatus(navigator.onLine ? "reconnecting" : "offline");
        // EventSource doesn't expose the status code; a 401 (no session
        // cookie, or the token changed) shows up as an error like any other.
        // Ask the server, and reconnect right away once signed in. Nobody
        // asked for this reconnect, so a closed dialog stays closed.
        void ensureSession(agentAPIUrl, {background: true}).then((signedIn) => {
          if (signedIn && !disposed) reconnectNow();
        });

        if (reconnectTimeoutRef.current) {
          clearTimeout(reconnectTimeoutRef.current);
        }
        const attempt = reconnectAttemptRef.current + 1;
        reconnectAttemptRef.current = attempt;
        setReconnectAttempt(attempt);
        const delay = getReconnectDelay(attempt);
        setNextReconnectAt(Date.now() + delay);
        reconnectTimeoutRef.current = setTimeout(() => {
          if (!disposed) {
            setNextReconnectAt(null);
            setupEventSource();
          }
        }, delay);
      };

      return eventSource;
    };

    // Show the cached transcript right away and connect from where it
    // left off. Without a cache (or if it is slow to read) connect anyway.
    let started = false;
    const start = () => {
      if (started || disposed) return;
      started = true;
      // Sign in first (a `#token=` link, or the dialog when the server
      // needs a token). Only the first connection waits for it.
      void bootstrapSession(agentAPIUrl).finally(() => {
        if (!disposed) setupEventSource();
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

    // Clean up on component unmount
    return () => {
      disposed = true;
      window.removeEventListener("offline", handleOffline);
      window.removeEventListener("online", handleOnline);
      document.removeEventListener("visibilitychange", handleVisible);
      window.removeEventListener("focus", handleVisible);
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
        reconnectTimeoutRef.current = null;
      }
      eventSourceRef.current?.close();
      eventSourceRef.current = null;
    };
  }, [agentAPIUrl, agentStatusRef, api, markSynced, queueMayHaveSent, reconnectNonce, reconnectNow, refreshQueue, scheduleFlush, setMessages, setRequests, setRichMessages, setTasks, setTransport, syncRef, updateAgentStatus]);

  return {connectionStatus, reconnectAttempt, nextReconnectAt, reconnectByUser, synced, markSynced};
}
