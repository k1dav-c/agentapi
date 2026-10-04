"use client";

import { useSearchParams } from "next/navigation";
import {
  useState,
  useEffect,
  useRef,
  useCallback,
  useMemo,
  createContext,
  PropsWithChildren,
  useContext,
} from "react";
import {toast} from "sonner";
import {getErrorMessage} from "@/lib/error-utils";
import {getDocumentTitle} from "@/lib/document-title";
import {getReconnectDelay} from "@/lib/reconnect";
import {parseFailedMessages} from "@/lib/failed-messages";
import {clearSession, loadSession, mergeByKey, saveSession} from "@/lib/session-cache";
import {
  createChatAPI,
  type MCPCheckResult,
  type MCPConfig,
  type MCPProfiles,
  type UploadOptions,
} from "@/lib/chat-api";

export interface Message {
  id: number;
  role: string;
  content: string;
  time?: string;
}

// Draft messages are used to optmistically update the UI
// before the server responds.
export interface DraftMessage extends Omit<Message, "id"> {
  id?: number;
  clientId: string;
  deliveryStatus: "sending" | "failed";
  error?: string;
}

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

// How long incoming conversation events are collected before they are
// applied together. A reload replays the whole session as one event per
// message; applying them one by one re-rendered the transcript each time.
const EVENT_FLUSH_MS = 32;
// Minimum time between writes of the conversation cache.
const CACHE_SAVE_INTERVAL_MS = 2000;
// How long to wait for the cache before connecting without it.
const CACHE_LOAD_TIMEOUT_MS = 400;
// While a reconnect replays state, updates are held until the replay is
// complete and applied at once; a very long replay still shows progress
// this often.
const REPLAY_FLUSH_MAX_MS = 1500;

const richMessageKey = (message: RichMessage) => `${message.message_id}\u0000${message.role}`;

// Drops the transport-only change sequence before an update is stored.
function withoutSeq<T extends {seq?: number}>(item: T): Omit<T, "seq"> {
  const copy = {...item};
  delete copy.seq;
  return copy;
}

export interface RichContentBlock {
  type: "text" | "thinking" | "tool_use" | "tool_result";
  text?: string;
  thinking?: string;
  tool_use_id?: string;
  tool_name?: string;
  tool_input?: unknown;
  status?: "running" | "completed" | "failed";
  is_error?: boolean;
}

export interface Usage {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_read_input_tokens: number;
}

export interface RichMessage {
  message_id: string;
  role: string;
  content: RichContentBlock[];
  timestamp: string;
  usage?: Usage;
  model?: string;
  stop_reason?: string;
}

interface StatusChangeEvent {
  status: string;
  agent_type: string;
  agentapi_version?: string;
  terminal_prompt?: string;
}

interface ErrorEventData {
  message: string;
  level: string;
  time: string;
}

function isDraftMessage(message: Message | DraftMessage): boolean {
  return message.id === undefined;
}

type MessageType = "user" | "raw";

export interface SendResult {
  ok: boolean;
  queued: boolean;
}

export type ServerStatus = "stable" | "running" | "offline" | "unknown";
export type ConnectionStatus = "connected" | "reconnecting" | "offline";

export interface FileUploadResponse {
  ok: boolean;
  filePath?: string;
  error?: string;
}

export interface QueuedMessage {
  id: number;
  content: string;
  time: string;
}

export type AgentType = "claude" | "goose" | "aider" | "gemini" | "amp" | "codex" | "cursor" | "cursor-agent" | "copilot" | "auggie" | "amazonq" | "opencode" | "kimi" | "pi" | "custom" | "unknown";

export type AgentColorDisplayNamePair = {
  displayName: string;
}

export const AgentType: Record<Exclude<AgentType, "unknown">, AgentColorDisplayNamePair> = {
  claude: {displayName: "Claude Code"},
  goose: {displayName: "Goose"},
  aider: {displayName: "Aider"},
  gemini: { displayName: "Gemini"},
  amp: {displayName: "Amp"},
  codex: {displayName: "Codex"},
  cursor: { displayName: "Cursor Agent"},
  "cursor-agent": { displayName: "Cursor Agent"},
  copilot: {displayName: "Copilot"},
  auggie: {displayName: "Auggie"},
  amazonq: {displayName: "Amazon Q"},
  opencode: {displayName: "Opencode"},
  kimi: {displayName: "Kimi Code"},
  pi: {displayName: "Pi"},
  custom: { displayName: "Custom"}
}

interface ChatContextValue {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  // Bottom of the terminal while the agent shows an interactive prompt
  // (selection list, confirmation) that must be answered; empty otherwise.
  terminalPrompt: string;
  loading: boolean;
  serverStatus: ServerStatus;
  connectionStatus: ConnectionStatus;
  queuedMessages: QueuedMessage[];
  sendMessage: (message: string, type?: MessageType) => Promise<SendResult>;
  // Sends keystrokes to the agent's terminal as-is (TTY mode).
  sendTerminalInput: (data: string) => Promise<void>;
  retryFailedMessage: (clientId: string) => Promise<boolean>;
  dismissFailedMessage: (clientId: string) => void;
  updateQueuedMessage: (id: number, content: string) => Promise<void>;
  deleteQueuedMessage: (id: number) => Promise<void>;
  uploadFiles: (
    formData: FormData,
    options?: UploadOptions,
  ) => Promise<FileUploadResponse>;
  reconnectAttempt: number;
  nextReconnectAt: number | null;
  reconnectNow: () => void;
  downloadSession: () => Promise<void>;
  deleteMessages: () => Promise<void>;
  getTemporal: ReturnType<typeof createChatAPI>["getTemporal"];
  updateTemporal: ReturnType<typeof createChatAPI>["updateTemporal"];
  getTemporalProfiles: ReturnType<typeof createChatAPI>["getTemporalProfiles"];
  saveTemporalProfile: ReturnType<typeof createChatAPI>["saveTemporalProfile"];
  importTemporalProfiles: ReturnType<typeof createChatAPI>["importTemporalProfiles"];
  deleteTemporalProfile: ReturnType<typeof createChatAPI>["deleteTemporalProfile"];
  applyTemporalProfile: ReturnType<typeof createChatAPI>["applyTemporalProfile"];
  getMCP: () => Promise<MCPConfig>;
  updateMCP: (
    servers: Record<string, unknown>,
    restart?: boolean,
  ) => Promise<MCPConfig>;
  checkMCP: (servers?: Record<string, unknown>) => Promise<MCPCheckResult[]>;
  createMCPServer: (name: string, config: unknown, restart?: boolean) => Promise<void>;
  updateMCPServer: (name: string, config: unknown, restart?: boolean) => Promise<void>;
  deleteMCPServer: (name: string, restart?: boolean) => Promise<void>;
  getMCPProfiles: () => Promise<MCPProfiles>;
  saveMCPProfile: (name: string, servers: Record<string, unknown>) => Promise<MCPProfiles>;
  deleteMCPProfile: (name: string) => Promise<MCPProfiles>;
  applyMCPProfile: (name: string, restart?: boolean) => Promise<void>;
  storageScope: string;
  agentType: AgentType;
  // AgentAPI server version (e.g. "0.14.0"); empty until the first status
  // event or when talking to a server that predates version reporting.
  agentapiVersion: string;
  customTitle?: string;
  workspaceUrl?: string;
}

// The server sends a heartbeat event every 15s. If nothing (heartbeat or
// otherwise) arrives for this long, the connection is considered dead even
// when the EventSource still reports itself as open — which happens when
// the TCP connection dies without a FIN, e.g. after system sleep.
const STALE_CONNECTION_MS = 45_000;

const ChatContext = createContext<ChatContextValue | undefined>(undefined);

export const useAgentAPIUrl = (): string => {
  const searchParams = useSearchParams();
  const paramsUrl = searchParams.get("url");
  if (paramsUrl) {
    return paramsUrl;
  }
  const basePath = process.env.NEXT_PUBLIC_BASE_PATH;
  if (!basePath) {
    throw new Error(
      "agentAPIUrl is not set. Please set the url query parameter to the URL of the AgentAPI or the NEXT_PUBLIC_BASE_PATH environment variable."
    );
  }
  // NOTE(cian): We use '../' here to construct the agent API URL relative
  // to the chat's location. Let's say the app is hosted on a subpath
  // `/@admin/workspace.agent/apps/ccw/`. When you visit this URL you get
  // redirected to `/@admin/workspace.agent/apps/ccw/chat/embed`. This serves
  // this React application, but it needs to know where the agent API is hosted.
  // This will be at the root of where the application is mounted e.g.
  // `/@admin/workspace.agent/apps/ccw/`. Previously we used
  // `window.location.origin` but this assumes that the application owns the
  // entire origin.
  // See: https://github.com/coder/coder/issues/18779#issuecomment-3133290494 for more context.
  let chatURL: string = new URL(basePath, window.location.origin).toString();
  // NOTE: trailing slashes and relative URLs are tricky.
  // https://developer.mozilla.org/en-US/docs/Web/API/URL_API/Resolving_relative_references#current_directory_relative
  if (!chatURL.endsWith("/")) {
    chatURL += "/";
  }
  const agentAPIURL = new URL("..", chatURL).toString();
  if (agentAPIURL.endsWith("/")) {
    return agentAPIURL.slice(0, -1);
  }
  return agentAPIURL;
};

export function ChatProvider({ children }: PropsWithChildren) {
  const searchParams = useSearchParams();
  const customTitle = searchParams.get("title") ?? undefined;
  const workspaceUrl = (() => {
    const value = searchParams.get("workspace");
    if (!value) return undefined;
    try {
      const url = new URL(value, window.location.origin);
      return url.protocol === "http:" || url.protocol === "https:" ? url.toString() : undefined;
    } catch {
      return undefined;
    }
  })();
  const [messages, setMessages] = useState<(Message | DraftMessage)[]>([]);
  const [richMessages, setRichMessages] = useState<RichMessage[]>([]);
  const [terminalPrompt, setTerminalPrompt] = useState("");
  const [loading, setLoading] = useState<boolean>(false);
  const [serverStatus, setServerStatus] = useState<ServerStatus>("unknown");
  const [connectionStatus, setConnectionStatus] =
    useState<ConnectionStatus>("reconnecting");
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([]);
  const [agentType, setAgentType] = useState<AgentType>("custom");
  const [agentapiVersion, setAgentapiVersion] = useState("");
  const eventSourceRef = useRef<EventSource | null>(null);
  const lastEventAtRef = useRef(Date.now());
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef(0);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  const [nextReconnectAt, setNextReconnectAt] = useState<number | null>(null);
  const [reconnectNonce, setReconnectNonce] = useState(0);
  const [failedMessagesHydrated, setFailedMessagesHydrated] = useState(false);
  const agentAPIUrl = useAgentAPIUrl();
  const api = useMemo(() => createChatAPI(agentAPIUrl), [agentAPIUrl]);
  const failedMessagesStorageKey = `agentapi.chat.failed-messages:${agentAPIUrl}`;
  // Server conversation state the client holds: its epoch and the highest
  // change sequence applied. Sent as /events?since=&epoch= so a reconnect
  // only receives what changed.
  const syncRef = useRef({epoch: "", seq: 0});
  // Events received but not applied yet; see EVENT_FLUSH_MS.
  const pendingRef = useRef({
    messages: new Map<number, Message & {seq?: number}>(),
    rich: new Map<string, RichMessage & {seq?: number}>(),
    replace: false,
  });
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cacheLoadedForRef = useRef("");
  // Highest seq received on this connection, and the seq the current replay
  // ends at (session_sync.seq) while it is still arriving.
  const receivedSeqRef = useRef(0);
  const replayEndRef = useRef<number | null>(null);

  const flushEvents = useCallback(() => {
    flushTimerRef.current = null;
    const pending = pendingRef.current;
    const {replace} = pending;
    const messageUpdates = [...pending.messages.values()];
    const richUpdates = [...pending.rich.values()];
    pending.messages = new Map();
    pending.rich = new Map();
    pending.replace = false;
    let applied = syncRef.current.seq;
    for (const update of [...messageUpdates, ...richUpdates]) applied = Math.max(applied, update.seq ?? 0);
    syncRef.current = {...syncRef.current, seq: applied};

    if (replace || messageUpdates.length > 0) {
      setMessages((previous) => {
        // A server message supersedes drafts that are still being sent;
        // failed drafts stay until they are retried or dismissed.
        const kept = previous.filter(
          (message) =>
            (replace ? isDraftMessage(message) : true) &&
            (!isDraftMessage(message) || (message as DraftMessage).deliveryStatus === "failed"),
        );
        return mergeByKey(
          kept,
          messageUpdates.map(withoutSeq),
          (message) => (isDraftMessage(message) ? undefined : String((message as Message).id)),
        );
      });
    }
    if (replace || richUpdates.length > 0) {
      setRichMessages((previous) =>
        mergeByKey(replace ? [] : previous, richUpdates.map(withoutSeq), richMessageKey),
      );
    }
  }, []);

  const scheduleFlush = useCallback((seq?: number) => {
    if (seq !== undefined) receivedSeqRef.current = Math.max(receivedSeqRef.current, seq);
    const replayEnd = replayEndRef.current;
    if (replayEnd !== null && receivedSeqRef.current >= replayEnd) {
      // The replay is complete: apply it now in one render.
      replayEndRef.current = null;
      if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
      flushTimerRef.current = setTimeout(flushEvents, 0);
      return;
    }
    if (flushTimerRef.current) return;
    flushTimerRef.current = setTimeout(
      flushEvents,
      replayEndRef.current !== null ? REPLAY_FLUSH_MAX_MS : EVENT_FLUSH_MS,
    );
  }, [flushEvents]);

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
      setQueuedMessages(queue);
    } catch {
      // The connection status handler reports connectivity failures.
    }
  }, [api]);
  const currentTask = [...messages]
    .reverse()
    .find((message) => message.role === "user")
    ?.content;

  useEffect(() => {
    document.title = getDocumentTitle({
      connectionStatus,
      serverStatus,
      terminalPrompt,
      task: currentTask,
      customTitle,
    });
  }, [connectionStatus, currentTask, customTitle, serverStatus, terminalPrompt]);

  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(failedMessagesStorageKey);
      if (!saved) {
        setFailedMessagesHydrated(true);
        return;
      }
      const failed = parseFailedMessages(saved) as DraftMessage[];
      setMessages((previous) => {
        const existing = new Set(
          previous
            .filter(isDraftMessage)
            .map((message) => (message as DraftMessage).clientId),
        );
        return [
          ...previous,
          ...failed.filter((message) => !existing.has(message.clientId)),
        ];
      });
    } catch {
      window.localStorage.removeItem(failedMessagesStorageKey);
    } finally {
      setFailedMessagesHydrated(true);
    }
  }, [failedMessagesStorageKey]);

  useEffect(() => {
    if (!failedMessagesHydrated) return;
    const failed = messages.filter(
      (message): message is DraftMessage =>
        isDraftMessage(message) &&
        (message as DraftMessage).deliveryStatus === "failed",
    );
    try {
      if (failed.length > 0) {
        window.localStorage.setItem(
          failedMessagesStorageKey,
          JSON.stringify(failed),
        );
      } else {
        window.localStorage.removeItem(failedMessagesStorageKey);
      }
    } catch {
      // Keep failed messages in memory when storage is unavailable.
    }
  }, [failedMessagesHydrated, failedMessagesStorageKey, messages]);

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
        setServerStatus("offline"); // Or some other appropriate status
        return null; // Don't try to connect if URL is empty
      }

      const {epoch, seq} = syncRef.current;
      const eventSource = new EventSource(
        epoch
          ? `${agentAPIUrl}/events?sync=1&since=${seq}&epoch=${encodeURIComponent(epoch)}`
          : `${agentAPIUrl}/events?sync=1`,
      );
      eventSourceRef.current = eventSource;
      // Servers that predate session_sync replay everything on every
      // connection without saying so; treat that replay as the full state.
      let synced = false;
      const assumeFullReplay = () => {
        if (synced) return;
        synced = true;
        syncRef.current = {epoch: "", seq: 0};
        pendingRef.current.messages.clear();
        pendingRef.current.rich.clear();
        pendingRef.current.replace = true;
      };

      eventSource.addEventListener("session_sync", (event) => {
        lastEventAtRef.current = Date.now();
        synced = true;
        const data: SessionSyncEvent = JSON.parse(event.data);
        const full = data.full || data.epoch !== syncRef.current.epoch;
        if (full) {
          // Everything that follows is the full state: replace what the
          // cache or an earlier connection left.
          pendingRef.current.messages.clear();
          pendingRef.current.rich.clear();
          pendingRef.current.replace = true;
          syncRef.current = {epoch: data.epoch, seq: 0};
        }
        receivedSeqRef.current = syncRef.current.seq;
        replayEndRef.current = data.seq > receivedSeqRef.current ? data.seq : null;
        if (full || replayEndRef.current === null) scheduleFlush();
      });

      // Server-sent keep-alive; only used to detect dead connections.
      eventSource.addEventListener("heartbeat", () => {
        lastEventAtRef.current = Date.now();
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
        scheduleFlush(data.seq);
      });

      eventSource.addEventListener("rich_message_update", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        const data: RichMessage & {seq?: number} = JSON.parse(event.data);
        pendingRef.current.rich.set(richMessageKey(data), data);
        scheduleFlush(data.seq);
      });

      // Handle status changes
      eventSource.addEventListener("status_change", (event) => {
        lastEventAtRef.current = Date.now();
        assumeFullReplay();
        const data: StatusChangeEvent = JSON.parse(event.data);
        if (data.status === "stable") {
          setServerStatus("stable");
        } else if (data.status === "running") {
          setServerStatus("running");
        } else {
          setServerStatus("unknown");
        }

        // Set agent type
        setAgentType(data.agent_type === "" ? "unknown" : data.agent_type as AgentType);
        setAgentapiVersion(data.agentapi_version ?? "");
        setTerminalPrompt(data.terminal_prompt ?? "");
        void refreshQueue();
      });

      // Handle agent error events
      eventSource.addEventListener("agent_error", (event) => {
        const messageEvent = event as MessageEvent;
        try {
          const data: ErrorEventData = JSON.parse(messageEvent.data);

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
      setupEventSource();
    };
    if (cacheLoadedForRef.current !== agentAPIUrl) {
      cacheLoadedForRef.current = agentAPIUrl;
      void loadSession<Message, RichMessage>(agentAPIUrl).then((cached) => {
        if (cached && !started && !disposed) {
          syncRef.current = {epoch: cached.epoch, seq: cached.seq};
          setMessages((previous) => [...cached.messages, ...previous.filter(isDraftMessage)]);
          setRichMessages(cached.richMessages);
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
  }, [agentAPIUrl, reconnectNonce, reconnectNow, refreshQueue, scheduleFlush]);

  // Keep the conversation cache current, at most every
  // CACHE_SAVE_INTERVAL_MS, and once more when the page is hidden.
  const saveRequestRef = useRef<() => void>(() => {});
  const latestRef = useRef({messages, richMessages});
  latestRef.current = {messages, richMessages};
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    let lastSave = 0;
    const save = () => {
      timer = null;
      lastSave = Date.now();
      const {epoch, seq} = syncRef.current;
      if (!epoch) return;
      void saveSession(agentAPIUrl, {
        epoch,
        seq,
        messages: latestRef.current.messages.filter((message) => !isDraftMessage(message)) as Message[],
        richMessages: latestRef.current.richMessages,
        savedAt: Date.now(),
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
  }, [messages, richMessages]);

  // Send a new message
  const sendMessage = async (
    content: string,
    type: "user" | "raw" = "user"
  ): Promise<SendResult> => {
    // For user messages, require non-empty content
    if (type === "user" && !content.trim()) return {ok: false, queued: false};
    const clientId = crypto.randomUUID();

    // For raw messages, don't set loading state as it's usually fast
    if (type === "user") {
      setMessages((prevMessages) => [
        ...prevMessages,
        {
          role: "user",
          content,
          clientId,
          deliveryStatus: "sending",
        },
      ]);
      setLoading(true);
    }

    try {
      const result = await api.sendMessage(content, type);
      await refreshQueue(true);
      if (type === "user") {
        setMessages((previous) =>
          previous.filter(
            (message) =>
              !isDraftMessage(message) ||
              (message as DraftMessage).clientId !== clientId,
          ),
        );
      }
      return {
        ok: result.ok,
        queued: result.queued,
      };
    } catch (error) {
      console.error("Error sending message:", error);
      const message = getErrorMessage(error)

      toast.error(`Error sending message`, {
        description: message,
      });
      if (type === "user") {
        setMessages((previous) =>
          previous.map((item) =>
            isDraftMessage(item) &&
            (item as DraftMessage).clientId === clientId
              ? {
                  ...(item as DraftMessage),
                  deliveryStatus: "failed",
                  error: message,
                }
              : item,
          ),
        );
      }
      return {ok: false, queued: false};
    } finally {
      if (type === "user") {
        setLoading(false);
      }
    }
  };

  const dismissFailedMessage = (clientId: string) => {
    setMessages((previous) =>
      previous.filter(
        (message) =>
          !isDraftMessage(message) ||
          (message as DraftMessage).clientId !== clientId,
      ),
    );
  };

  const retryFailedMessage = async (clientId: string) => {
    const failedMessage = messages.find(
      (message) =>
        isDraftMessage(message) &&
        (message as DraftMessage).clientId === clientId,
    );
    if (!failedMessage) return false;
    dismissFailedMessage(clientId);
    const result = await sendMessage(failedMessage.content, "user");
    return result.ok;
  };

  // Upload files to workspace
  const uploadFiles = (formData: FormData, options: UploadOptions = {}) =>
    api.uploadFiles(formData, options);

  const updateQueuedMessage = async (id: number, content: string) => {
    await api.updateQueuedMessage(id, content);
    await refreshQueue(true);
  };

  const deleteQueuedMessage = async (id: number) => {
    await api.deleteQueuedMessage(id);
    await refreshQueue(true);
  };

  const downloadSession = async () => {
    try {
      const events = await api.getTimelineEvents();
      const jsonl = events.map((event) => JSON.stringify(event)).join("\n");
      const blob = new Blob([jsonl === "" ? "" : `${jsonl}\n`], {
        type: "application/x-ndjson",
      });
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      const timestamp = new Date().toISOString().replaceAll(":", "-");
      link.href = url;
      link.download = `agentapi-session-${timestamp}.jsonl`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
      toast.success("Session JSONL downloaded");
    } catch (error) {
      toast.error("Session download failed", {
        description: getErrorMessage(error),
      });
      throw error;
    }
  };

  return (
    <ChatContext.Provider
      value={{
        messages,
        richMessages,
        terminalPrompt,
        loading,
        sendMessage,
        sendTerminalInput: async (data: string) => {
          await api.sendMessage(data, "raw");
        },
        retryFailedMessage,
        dismissFailedMessage,
        serverStatus,
        connectionStatus,
        queuedMessages,
        updateQueuedMessage,
        deleteQueuedMessage,
        uploadFiles,
        reconnectAttempt,
        nextReconnectAt,
        reconnectNow,
        downloadSession,
        deleteMessages: async () => {
          await api.deleteMessages();
          syncRef.current = {epoch: "", seq: 0};
          void clearSession(agentAPIUrl);
          setMessages([]);
          setRichMessages([]);
        },
        getTemporal: api.getTemporal,
        updateTemporal: api.updateTemporal,
        getTemporalProfiles: api.getTemporalProfiles,
        saveTemporalProfile: api.saveTemporalProfile,
        importTemporalProfiles: api.importTemporalProfiles,
        deleteTemporalProfile: api.deleteTemporalProfile,
        applyTemporalProfile: api.applyTemporalProfile,
        getMCP: api.getMCP,
        updateMCP: api.updateMCP,
        checkMCP: api.checkMCP,
        createMCPServer: api.createMCPServer,
        updateMCPServer: api.updateMCPServer,
        deleteMCPServer: api.deleteMCPServer,
        getMCPProfiles: api.getMCPProfiles,
        saveMCPProfile: api.saveMCPProfile,
        deleteMCPProfile: api.deleteMCPProfile,
        applyMCPProfile: api.applyMCPProfile,
        storageScope: agentAPIUrl,
        agentType,
        agentapiVersion,
        customTitle,
        workspaceUrl,
      }}
    >
      {children}
    </ChatContext.Provider>
  );
}

export function useChat() {
  const context = useContext(ChatContext);
  if (!context) {
    throw new Error("useChat must be used within a ChatProvider");
  }
  return context;
}
