"use client";

import {useCallback, useEffect, useRef, useState, type Dispatch, type RefObject, type SetStateAction} from "react";
import {toast} from "sonner";
import {getReconnectDelay} from "@/lib/reconnect";
import {loadSession, mergeByKey} from "@/lib/session-cache";
import {bootstrapSession, ensureSession, withCredentials} from "@/lib/auth";
import type {createChatAPI} from "@/lib/chat-api";
import {parseTransport, type Transport} from "@/lib/dispatch";
import {isLoginNotice} from "@/lib/login";
import {mergeRequests, type NativeRequest} from "@/lib/native-requests";
import type {SyncTracker} from "@/lib/session-sync";
import {applyStatusChange, update, type AgentStatus, type StatusChangeEvent} from "@/lib/agent-status";
import {isDraftMessage, richMessageKey, withoutSeq, type DraftMessage, type Message, type RichMessage} from "@/lib/conversation";
import type {ConnectionStatus} from "./chat-provider";
import {agentErrorKey} from "@/lib/agent-errors";

interface MessageUpdateEvent {
  id: number;
  role: string;
  message: string;
  time: string;
  seq?: number;
}

interface SessionSyncEvent {
  epoch: string;
  full: boolean;
  seq: number;
}

interface ErrorEventData {
  message: string;
  level: string;
  time: string;
}

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

// useEventStream keeps the chat in step with the server's /events. It
// shows the cached transcript first and connects from where the cache left
// off; applies the events in batches (EVENT_FLUSH_MS), a replay at once
// when it is complete (SyncTracker); and reconnects with backoff when the
// connection drops, goes stale or the browser comes back online.
export function useEventStream({
  agentAPIUrl,
  api,
  syncRef,
  setMessages,
  setRichMessages,
  setRequests,
  setTransport,
  updateAgentStatus,
  agentStatusRef,
  refreshQueue,
}: {
  agentAPIUrl: string;
  api: ReturnType<typeof createChatAPI>;
  syncRef: RefObject<SyncTracker>;
  setMessages: Dispatch<SetStateAction<(Message | DraftMessage)[]>>;
  setRichMessages: Dispatch<SetStateAction<RichMessage[]>>;
  setRequests: Dispatch<SetStateAction<NativeRequest[]>>;
  setTransport: Dispatch<SetStateAction<Transport>>;
  updateAgentStatus: (change: (status: AgentStatus) => AgentStatus) => void;
  agentStatusRef: RefObject<AgentStatus>;
  refreshQueue: (force?: boolean) => Promise<void>;
}) {
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>("reconnecting");
  // Errors the server replays on every connection that this page showed
  // already.
  const shownErrorsRef = useRef(new Set<string>());
  const eventSourceRef = useRef<EventSource | null>(null);
  const lastEventAtRef = useRef(Date.now());
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef(0);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  const [nextReconnectAt, setNextReconnectAt] = useState<number | null>(null);
  const [reconnectNonce, setReconnectNonce] = useState(0);
  // Events received but not applied yet; see EVENT_FLUSH_MS.
  const pendingRef = useRef({
    messages: new Map<number, Message & {seq?: number}>(),
    rich: new Map<string, RichMessage & {seq?: number}>(),
    requests: new Map<string, NativeRequest>(),
    replace: false,
  });
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cacheLoadedForRef = useRef("");

  const flushEvents = useCallback(() => {
    flushTimerRef.current = null;
    const pending = pendingRef.current;
    const {replace} = pending;
    const messageUpdates = [...pending.messages.values()];
    const richUpdates = [...pending.rich.values()];
    const requestUpdates = [...pending.requests.values()];
    pending.messages = new Map();
    pending.rich = new Map();
    pending.requests = new Map();
    pending.replace = false;
    for (const update of [...messageUpdates, ...richUpdates, ...requestUpdates]) syncRef.current.applied(update.seq ?? 0);

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
    if (replace || requestUpdates.length > 0) {
      // Requests keep their seq: it decides between an event and a state
      // read from the REST API (see mergeRequests).
      setRequests((previous) => mergeRequests(replace ? [] : previous, requestUpdates));
    }
  }, [setMessages, setRequests, setRichMessages, syncRef]);

  // Schedules applying the events received so far. An event that completes
  // the replay (see SyncTracker) applies the replay now, in one render.
  const scheduleFlush = useCallback((event?: MessageEvent, immediate = false) => {
    if (event && syncRef.current.noteEvent(event.lastEventId)) {
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

    // Function to create and set up EventSource
    const setupEventSource = () => {
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
        pendingRef.current.messages.clear();
        pendingRef.current.rich.clear();
        pendingRef.current.requests.clear();
        pendingRef.current.replace = true;
      };

      eventSource.addEventListener("session_sync", (event) => {
        lastEventAtRef.current = Date.now();
        synced = true;
        const data: SessionSyncEvent = JSON.parse(event.data);
        const full = syncRef.current.begin(data, event.lastEventId);
        if (full) {
          // Everything that follows is the full state: replace what the
          // cache or an earlier connection left.
          pendingRef.current.messages.clear();
          pendingRef.current.rich.clear();
          pendingRef.current.requests.clear();
          pendingRef.current.replace = true;
        }
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
        const data: MessageUpdateEvent = JSON.parse(event.data);
        pendingRef.current.messages.set(data.id, {
          role: data.role,
          content: data.message,
          id: data.id,
          time: data.time,
          seq: data.seq,
        } as Message & {seq?: number});
        scheduleFlush(event);
      });

      eventSource.addEventListener("rich_message_update", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        const data: RichMessage & {seq?: number} = JSON.parse(event.data);
        pendingRef.current.rich.set(richMessageKey(data), data);
        scheduleFlush(event);
      });

      // A request the agent waits on (native transport) opened or changed.
      // Requests are part of the replay, like messages. Outside a replay one
      // applies at once: the status_change the server sends right after it
      // (now "stable", waiting) must not show before the request does.
      eventSource.addEventListener("request_update", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        const data: NativeRequest = JSON.parse(event.data);
        pendingRef.current.requests.set(data.id, data);
        scheduleFlush(event, true);
      });

      // Handle status changes
      eventSource.addEventListener("status_change", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        const data: StatusChangeEvent = JSON.parse(event.data);
        // The replay's last event may be its status.
        if (syncRef.current.replaying) scheduleFlush(event);
        updateAgentStatus((status) => applyStatusChange(status, data));
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
        void refreshQueue();
      });

      // Handle agent error events
      eventSource.addEventListener("agent_error", (event) => {
        const messageEvent = event as MessageEvent;
        if (syncRef.current.replaying) scheduleFlush(messageEvent);
        try {
          const data: ErrorEventData = JSON.parse(messageEvent.data);
          const key = agentErrorKey(data);
          if (shownErrorsRef.current.has(key)) return;
          shownErrorsRef.current.add(key);
          // The login card says it while the login is needed; afterwards
          // the notice is stale.
          if (isLoginNotice(data.message, agentStatusRef.current.loginRequired)) return;

          // Display error as toast notification that persists until manually dismissed
          if (data.level === "error") {
            toast.error(data.message, { duration: Infinity });
          } else if (data.level === "warning") {
            toast.warning(data.message, { duration: Infinity });
          } else {
            toast.info(data.message, { duration: Infinity });
          }
        } catch (e) {
          console.error("Failed to parse agent_error event data:", e);
        }
      });

      // Handle connection open (server is online)
      eventSource.onopen = () => {
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
        console.error("EventSource error:", error);
        setConnectionStatus(navigator.onLine ? "reconnecting" : "offline");
        eventSource.close();
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
      void loadSession<Message, RichMessage, NativeRequest>(agentAPIUrl).then((cached) => {
        if (cached && !started && !disposed) {
          syncRef.current.restore(cached.epoch, cached.seq);
          setMessages((previous) => [...cached.messages, ...previous.filter(isDraftMessage)]);
          setRichMessages(cached.richMessages);
          setRequests(cached.requests);
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
  }, [agentAPIUrl, agentStatusRef, api, reconnectNonce, reconnectNow, refreshQueue, scheduleFlush, setMessages, setRequests, setRichMessages, setTransport, syncRef, updateAgentStatus]);

  return {connectionStatus, reconnectAttempt, nextReconnectAt, reconnectByUser};
}
