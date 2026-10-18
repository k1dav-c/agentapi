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
import {bootstrapSession, ensureSession, withCredentials} from "@/lib/auth";
import {AuthDialog} from "@/components/auth-dialog";
import {
  createChatAPI,
  httpURL,
  type AgentSettings,
  type AgentUsage,
  type FileContent,
  type FileDiff,
  type FilePreview,
  type MCPCheckResult,
  type MCPConfig,
  type MCPProfiles,
  type UploadOptions,
} from "@/lib/chat-api";
import {features, type Features} from "@/lib/features";
import {
  canStop,
  parseTransport,
  type Capabilities,
  type DispatchContext,
  type DispatchMode,
  type Transport,
} from "@/lib/dispatch";
import {
  mergeListedRequests,
  mergeRequests,
  pendingRequests,
  type NativeRequest,
  type RequestResponse,
} from "@/lib/native-requests";
import {isLoginNotice, parseLoginRequired, type LoginRequired} from "@/lib/login";
import {getAgentState, type AgentStateKind} from "@/lib/session-status";
import {SyncTracker} from "@/lib/session-sync";

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
  // Images in a tool result (an image file the agent read), served by
  // AgentAPI at url.
  images?: {media_type: string; url?: string}[];
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
  lifecycle?: string;
  agent_type: string;
  // Missing from servers that predate it; GET /status says then.
  transport?: string;
  agentapi_version?: string;
  terminal_columns?: number;
  session_name?: string;
  terminal_prompt?: string;
  // Native transport only. The chat counts the pending requests itself,
  // from the requests it holds (pending_requests is ignored).
  capabilities?: Capabilities;
  login_required?: unknown;
  // Native agents with the settings capability.
  settings?: AgentSettings;
  // Native agents, once they report it.
  usage?: AgentUsage;
  // A shell terminal is available (native transport).
  terminal?: boolean;
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
  // The message was added to the turn the agent is working on.
  steered?: boolean;
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

// parseAgentType is the agent type a server reports: "unknown" for none,
// and "custom" for one this build of the chat doesn't know (a newer
// server), so looking up its name can't fail.
export function parseAgentType(value: string | undefined): AgentType {
  if (!value) return "unknown";
  return Object.hasOwn(AgentType, value) ? (value as AgentType) : "custom";
}

// agentDisplayName names an agent type, or says fallback for "unknown".
export function agentDisplayName(type: AgentType, fallback: string): string {
  return type === "unknown" ? fallback : (AgentType[type]?.displayName ?? fallback);
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
  // How the server drives the agent; "pty" until the server says otherwise.
  transport: Transport;
  // Optional operations of a native agent; null for other transports.
  capabilities: Capabilities | null;
  // What the chat offers for the transport and the agent (lib/features).
  features: Features;
  // Number of requests the agent waits on (native transport), counted from
  // requests so that both always agree.
  pendingRequestCount: number;
  // Set while the agent waits for someone to log it in (native transport).
  loginRequired: LoginRequired | null;
  // The permission mode, model and effort of a native agent that can change
  // them (capabilities.settings); null otherwise.
  agentSettings: AgentSettings | null;
  // GET /settings: the settings and the values they can take.
  getSettings: ReturnType<typeof createChatAPI>["getSettings"];
  // PUT /settings: changes the settings set in change.
  updateSettings: (change: AgentSettings) => ReturnType<ReturnType<typeof createChatAPI>["updateSettings"]>;
  // How full the context window is and the account's rate limits, as the
  // agent last reported them (native transport); null until it does.
  agentUsage: AgentUsage | null;
  // Whether the server offers a shell terminal (native transport, GET
  // /terminal), and typing into and resizing it.
  terminalAvailable: boolean;
  // POST /compact (agents with the compact capability).
  compact: () => Promise<void>;
  terminalInput: (data: string) => Promise<void>;
  terminalResize: (cols: number, rows: number) => Promise<void>;
  // The agent is starting (lifecycle "starting"): there is no turn yet, and
  // while it finds no login AgentAPI starts it again by itself.
  agentStarting: boolean;
  // What the agent is doing from the user's point of view ("Needs you",
  // "Working", ...), for every view that shows it.
  agentState: AgentStateKind;
  // What the composer may offer: steer, interrupt (see lib/dispatch).
  dispatch: DispatchContext;
  // Checks the login again now (POST /login/retry).
  retryLogin: () => Promise<void>;
  // Requests the agent made (native transport): pending ones and the most
  // recently closed, oldest first.
  requests: NativeRequest[];
  respondToRequest: (id: string, response: RequestResponse) => Promise<NativeRequest>;
  // Whether Stop can stop what the agent is doing now, and stopping it
  // (Esc on a PTY agent's terminal, POST /interrupt on the native
  // transport). stop reports a failure itself and resolves to false.
  canStop: boolean;
  stop: () => Promise<boolean>;
  steerQueuedMessage: (id: number) => Promise<void>;
  reorderQueue: (ids: number[]) => Promise<void>;
  sendMessage: (message: string, type?: MessageType, mode?: DispatchMode) => Promise<SendResult>;
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
  // Reconnects right away, as the user asked: also asks for the API token
  // again if the user closed the sign-in dialog.
  reconnectNow: () => void;
  downloadSession: () => Promise<void>;
  deleteMessages: () => Promise<void>;
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
  // A file in the agent's working directory and its uncommitted changes,
  // for the file viewer (GET /files/content, GET /files/diff).
  getFileContent: (path: string) => Promise<FileContent>;
  getFileDiff: (path: string) => Promise<FileDiff>;
  getFilePreview: (path: string) => Promise<FilePreview>;
  fileImageURL: (path: string, version?: number) => string;
  storageScope: string;
  agentType: AgentType;
  // AgentAPI server version (e.g. "0.14.0"); empty until the first status
  // event or when talking to a server that predates version reporting.
  agentapiVersion: string;
  // Width of the agent's terminal; 0 until the server reports it.
  terminalColumns: number;
  // What the agent calls the session (Claude Code's title, Codex's thread
  // name, Pi's /name); empty until it names it.
  sessionName: string;
  customTitle?: string;
  // The Coder workspace: the ?workspace= parameter, or what the server says
  // (GET /workspace).
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
  const workspaceValue = searchParams.get("workspace");
  const workspaceParam = workspaceValue ? httpURL(workspaceValue, window.location.origin) : undefined;
  const [serverWorkspaceUrl, setServerWorkspaceUrl] = useState<string>();
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
  const [terminalColumns, setTerminalColumns] = useState(0);
  const [sessionName, setSessionName] = useState("");
  const [transport, setTransport] = useState<Transport>("pty");
  const [capabilities, setCapabilities] = useState<Capabilities | null>(null);
  const [agentSettings, setAgentSettings] = useState<AgentSettings | null>(null);
  const [agentUsage, setAgentUsage] = useState<AgentUsage | null>(null);
  const [terminalAvailable, setTerminalAvailable] = useState(false);
  const [loginRequired, setLoginRequiredState] = useState<LoginRequired | null>(null);
  // The login as the event handlers see it, to skip errors the card shows.
  const loginRef = useRef<LoginRequired | null>(null);
  const setLoginRequired = useCallback((login: LoginRequired | null) => {
    loginRef.current = login;
    setLoginRequiredState(login);
  }, []);
  const [lifecycle, setLifecycle] = useState("");
  const [requests, setRequests] = useState<NativeRequest[]>([]);
  const pendingRequestCount = useMemo(() => pendingRequests(requests).length, [requests]);
  const agentStarting = lifecycle === "starting";
  const agentState = getAgentState({
    serverStatus,
    connectionStatus,
    terminalPrompt,
    pendingRequestCount,
    loginRequired: loginRequired !== null,
  });
  const dispatch = useMemo<DispatchContext>(
    () => ({transport, capabilities: agentStarting ? null : capabilities, serverStatus, pendingRequestCount}),
    [agentStarting, capabilities, pendingRequestCount, serverStatus, transport],
  );
  const chatFeatures = useMemo(() => features(transport, capabilities, terminalAvailable), [capabilities, terminalAvailable, transport]);
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
  const [failedMessagesHydrated, setFailedMessagesHydrated] = useState(false);
  const agentAPIUrl = useAgentAPIUrl();
  const api = useMemo(() => createChatAPI(agentAPIUrl), [agentAPIUrl]);
  const failedMessagesStorageKey = `agentapi.chat.failed-messages:${agentAPIUrl}`;
  // Server conversation state the client holds: its epoch and the highest
  // change sequence applied. Sent as /events?since=&epoch= so a reconnect
  // only receives what changed.
  const syncRef = useRef(new SyncTracker());
  // Events received but not applied yet; see EVENT_FLUSH_MS.
  const pendingRef = useRef({
    messages: new Map<number, Message & {seq?: number}>(),
    rich: new Map<string, RichMessage & {seq?: number}>(),
    requests: new Map<string, NativeRequest>(),
    replace: false,
  });
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const cacheLoadedForRef = useRef("");
  // Highest seq received on this connection, and the seq the current replay
  // ends at (session_sync.seq) while it is still arriving.

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
    if (replace || requestUpdates.length > 0) {
      // Requests keep their seq: it decides between an event and a state
      // read from the REST API (see mergeRequests).
      setRequests((previous) => mergeRequests(replace ? [] : previous, requestUpdates));
    }
  }, []);

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
  // Reconnect now, as the user asked. Once the user closed the sign-in
  // dialog, reconnects in the background stop asking for the token; this
  // asks again, and reconnects once signed in.
  const reconnectByUser = useCallback(() => {
    reconnectNow();
    void ensureSession(agentAPIUrl).then((signedIn) => {
      if (signedIn) reconnectNow();
    });
  }, [agentAPIUrl, reconnectNow]);
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

  // Rendered as <title> rather than set on document.title from an effect:
  // Next re-applies the layout's static title after hydration, which
  // overwrote the state until it changed again.
  const documentTitle = getDocumentTitle({agentState, task: currentTask, customTitle});

  // The Coder workspace link, when the URL doesn't give one: asked on every
  // connection, so it also shows once the user signed in.
  useEffect(() => {
    if (workspaceParam || connectionStatus !== "connected") return;
    let active = true;
    api.getWorkspaceUrl().then(
      (url) => {
        if (active) setServerWorkspaceUrl(url);
      },
      () => undefined,
    );
    return () => {
      active = false;
    };
  }, [api, connectionStatus, workspaceParam]);

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
        if (data.status === "stable") {
          setServerStatus("stable");
        } else if (data.status === "running") {
          setServerStatus("running");
        } else {
          setServerStatus("unknown");
        }

        // Set agent type
        setAgentType(parseAgentType(data.agent_type));
        setAgentapiVersion(data.agentapi_version ?? "");
        setTerminalColumns(data.terminal_columns ?? 0);
        setSessionName(data.session_name ?? "");
        setTerminalPrompt(data.terminal_prompt ?? "");
        setCapabilities(data.capabilities ?? null);
        setAgentSettings(data.settings ?? null);
        setAgentUsage(data.usage ?? null);
        setTerminalAvailable(data.terminal ?? false);
        setLoginRequired(parseLoginRequired(data.login_required));
        setLifecycle(data.lifecycle ?? "");
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
          // The server sends its stored errors again on every connection.
          const key = `${data.time}\u0000${data.message}`;
          if (shownErrorsRef.current.has(key)) return;
          shownErrorsRef.current.add(key);
          // The login card says it while the login is needed; afterwards
          // the notice is stale.
          if (isLoginNotice(data.message, loginRef.current)) return;

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
  }, [agentAPIUrl, api, reconnectNonce, reconnectNow, refreshQueue, scheduleFlush, setLoginRequired]);

  // Keep the conversation cache current, at most every
  // CACHE_SAVE_INTERVAL_MS, and once more when the page is hidden.
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
        messages: latestRef.current.messages.filter((message) => !isDraftMessage(message)) as Message[],
        richMessages: latestRef.current.richMessages,
        requests: latestRef.current.requests,
        transport: latestRef.current.transport,
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
  }, [messages, richMessages, requests]);

  // Send a new message
  const sendMessage = async (
    content: string,
    type: "user" | "raw" = "user",
    mode: DispatchMode = "queue",
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
      const result = await api.sendMessage(content, type, mode);
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
        steered: result.steered,
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

  // Reads the requests from the REST API; see mergeListedRequests.
  const refreshRequests = async () => {
    const heldWhenAsked = latestRef.current.requests;
    const listed = await api.getRequests();
    setRequests((previous) => mergeListedRequests(previous, listed, heldWhenAsked));
  };

  const respondToRequest = async (id: string, response: RequestResponse) => {
    try {
      const state = await api.respondRequest(id, response);
      setRequests((previous) => mergeRequests(previous, [state]));
      return state;
    } catch (error) {
      // Whatever went wrong, show the request as the server has it now.
      void refreshRequests().catch(() => undefined);
      throw error;
    }
  };

  const stoppable = canStop(dispatch);
  const stop = async () => {
    if (!stoppable) return false;
    try {
      if (dispatch.transport === "native") {
        await api.interrupt();
        toast.info("Stopping the agent");
      } else {
        await api.sendMessage("\x1b", "raw");
        toast.info("Stop signal sent");
      }
      return true;
    } catch (error) {
      toast.error("Failed to stop the agent", {description: getErrorMessage(error)});
      return false;
    }
  };

  const retryLogin = async () => {
    const outcome = await api.retryLogin();
    if (outcome === "not-waiting") {
      // The status is out of date: the agent doesn't wait any more.
      const status = await api.getStatus();
      setLoginRequired(status.login_required);
    }
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
        transport,
        capabilities,
        features: chatFeatures,
        pendingRequestCount,
        loginRequired,
        agentSettings,
        agentUsage,
        terminalAvailable,
        compact: api.compact,
        terminalInput: api.terminalInput,
        terminalResize: api.terminalResize,
        getSettings: api.getSettings,
        updateSettings: async (change: AgentSettings) => {
          const result = await api.updateSettings(change);
          // Shown at once; the status_change event that follows says the same.
          setAgentSettings(result.settings);
          return result;
        },
        agentStarting,
        agentState,
        dispatch,
        retryLogin,
        requests,
        respondToRequest,
        canStop: stoppable,
        stop,
        steerQueuedMessage,
        reorderQueue,
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
        reconnectNow: reconnectByUser,
        downloadSession,
        deleteMessages: async () => {
          const epoch = syncRef.current.epoch;
          await api.deleteMessages();
          // The server's reset arrived as a session_sync of a new epoch,
          // with what the new session sent since (a restart can take
          // minutes): clearing now would drop both. Only a server that
          // doesn't send it needs clearing here.
          if (syncRef.current.epoch !== epoch) return;
          syncRef.current.reset();
          void clearSession(agentAPIUrl);
          setMessages([]);
          setRichMessages([]);
          setRequests([]);
        },
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
        getFileContent: api.getFileContent,
        getFileDiff: api.getFileDiff,
        getFilePreview: api.getFilePreview,
        fileImageURL: api.fileImageURL,
        storageScope: agentAPIUrl,
        agentType,
        agentapiVersion,
        terminalColumns,
        sessionName,
        customTitle,
        workspaceUrl: workspaceParam ?? serverWorkspaceUrl,
      }}
    >
      <title>{documentTitle}</title>
      {children}
      <AuthDialog />
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
