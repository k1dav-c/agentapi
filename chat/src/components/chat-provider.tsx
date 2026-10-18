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
  useLayoutEffect,
} from "react";
import {toast} from "sonner";
import {getErrorMessage} from "@/lib/error-utils";
import {getDocumentTitle} from "@/lib/document-title";
import {parseFailedMessages} from "@/lib/failed-messages";
import {clearSession} from "@/lib/session-cache";
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
import type {AgentType} from "@/lib/agents";
import {initialAgentStatus, update, type AgentStatus, type ServerStatus} from "@/lib/agent-status";
import {
  canStop,
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
import type {LoginRequired} from "@/lib/login";
import {getAgentState, type AgentStateKind} from "@/lib/session-status";
import {SyncTracker} from "@/lib/session-sync";
import {isDraftMessage, type DraftMessage, type Message, type RichMessage} from "@/lib/conversation";
import {useEventStream} from "./use-event-stream";
import {useQueue} from "./use-queue";
import {useSessionCache} from "./use-session-cache";
import {Store, useStoreSelector} from "@/lib/store";
import {useStableCallback} from "@/lib/use-stable-callback";
import {usePersisted} from "@/lib/use-persisted";

export type {DraftMessage, Message, RichContentBlock, RichMessage, Usage} from "@/lib/conversation";

type MessageType = "user" | "raw";

export interface SendResult {
  ok: boolean;
  queued: boolean;
  // The message was added to the turn the agent is working on.
  steered?: boolean;
}

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

const ChatContext = createContext<ChatContextValue | undefined>(undefined);
const ChatStoreContext = createContext<Store<ChatContextValue> | undefined>(undefined);

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
  const [loading, setLoading] = useState<boolean>(false);
  const [transport, setTransport] = useState<Transport>("pty");
  // The agent's status, from status_change events (lib/agent-status). An
  // event that changes nothing leaves it as it is; the ref has the latest,
  // for the event handlers.
  const [agentStatus, setAgentStatus] = useState<AgentStatus>(initialAgentStatus);
  const agentStatusRef = useRef(agentStatus);
  const updateAgentStatus = useCallback((change: (status: AgentStatus) => AgentStatus) => {
    const next = change(agentStatusRef.current);
    if (next === agentStatusRef.current) return;
    agentStatusRef.current = next;
    setAgentStatus(next);
  }, []);
  const {
    serverStatus,
    agentType,
    agentapiVersion,
    terminalColumns,
    sessionName,
    terminalPrompt,
    capabilities,
    settings: agentSettings,
    usage: agentUsage,
    terminalAvailable,
    loginRequired,
    lifecycle,
  } = agentStatus;
  const [requests, setRequests] = useState<NativeRequest[]>([]);
  const agentAPIUrl = useAgentAPIUrl();
  const api = useMemo(() => createChatAPI(agentAPIUrl), [agentAPIUrl]);
  // Server conversation state the client holds: its epoch and the highest
  // change sequence applied. Sent as /events?since=&epoch= so a reconnect
  // only receives what changed.
  const syncRef = useRef(new SyncTracker());
  // The requests as last rendered, for refreshRequests.
  const requestsRef = useRef(requests);
  requestsRef.current = requests;
  const {queuedMessages, refreshQueue, updateQueuedMessage, deleteQueuedMessage, steerQueuedMessage, reorderQueue} = useQueue(api);
  const {connectionStatus, reconnectAttempt, nextReconnectAt, reconnectByUser} = useEventStream({
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
  });
  useSessionCache({agentAPIUrl, syncRef, messages, richMessages, requests, transport});
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
  const failedMessagesStorageKey = `agentapi.chat.failed-messages:${agentAPIUrl}`;
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

  // Messages that failed to send survive a reload, to be retried.
  usePersisted(
    failedMessagesStorageKey,
    messages,
    (current) => {
      const failed = current.filter(
        (message): message is DraftMessage => isDraftMessage(message) && message.deliveryStatus === "failed",
      );
      return failed.length > 0 ? JSON.stringify(failed) : null;
    },
    (stored) => {
      if (!stored) return;
      const failed = parseFailedMessages(stored) as DraftMessage[];
      setMessages((previous) => {
        const existing = new Set(previous.filter(isDraftMessage).map((message) => message.clientId));
        return [...previous, ...failed.filter((message) => !existing.has(message.clientId))];
      });
    },
  );

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
              message.clientId !== clientId,
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
            isDraftMessage(item) && item.clientId === clientId
              ? {
                  ...item,
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
          message.clientId !== clientId,
      ),
    );
  };

  const retryFailedMessage = async (clientId: string) => {
    const failedMessage = messages.find(
      (message) =>
        isDraftMessage(message) &&
        message.clientId === clientId,
    );
    if (!failedMessage) return false;
    dismissFailedMessage(clientId);
    const result = await sendMessage(failedMessage.content, "user");
    return result.ok;
  };

  // Upload files to workspace
  const uploadFiles = (formData: FormData, options: UploadOptions = {}) =>
    api.uploadFiles(formData, options);

  // Reads the requests from the REST API; see mergeListedRequests.
  const refreshRequests = async () => {
    const heldWhenAsked = requestsRef.current;
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
      updateAgentStatus((current) => update(current, {loginRequired: status.login_required}));
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

  // Actions keep their identity from render to render, so the components
  // that take them don't render again when the state changes.
  const actions = {
    sendMessage: useStableCallback(sendMessage),
    sendTerminalInput: useStableCallback(async (data: string) => {
      await api.sendMessage(data, "raw");
    }),
    updateSettings: useStableCallback(async (change: AgentSettings) => {
      const result = await api.updateSettings(change);
      // Shown at once; the status_change event that follows says the same.
      updateAgentStatus((status) => update(status, {settings: result.settings}));
      return result;
    }),
    retryLogin: useStableCallback(retryLogin),
    respondToRequest: useStableCallback(respondToRequest),
    stop: useStableCallback(stop),
    steerQueuedMessage: useStableCallback(steerQueuedMessage),
    reorderQueue: useStableCallback(reorderQueue),
    retryFailedMessage: useStableCallback(retryFailedMessage),
    dismissFailedMessage: useStableCallback(dismissFailedMessage),
    updateQueuedMessage: useStableCallback(updateQueuedMessage),
    deleteQueuedMessage: useStableCallback(deleteQueuedMessage),
    uploadFiles: useStableCallback(uploadFiles),
    reconnectNow: useStableCallback(reconnectByUser),
    downloadSession: useStableCallback(downloadSession),
    deleteMessages: useStableCallback(async () => {
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
    }),
  };
  const value: ChatContextValue = {
    ...actions,
    messages,
    richMessages,
    terminalPrompt,
    loading,
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
    agentStarting,
    agentState,
    dispatch,
    requests,
    canStop: stoppable,
    serverStatus,
    connectionStatus,
    queuedMessages,
    reconnectAttempt,
    nextReconnectAt,
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
  };
  // Components that read a part of the value with useChatSelector render
  // again only when that part changes; the value is handed over once this
  // render is committed.
  const storeRef = useRef<Store<ChatContextValue> | null>(null);
  storeRef.current ??= new Store(value);
  const store = storeRef.current;
  useLayoutEffect(() => store.set(value));

  return (
    <ChatStoreContext.Provider value={store}>
      <ChatContext.Provider value={value}>
        <title>{documentTitle}</title>
        {children}
        <AuthDialog />
      </ChatContext.Provider>
    </ChatStoreContext.Provider>
  );
}

// useChat returns all of the chat's state and actions; the component
// renders again whenever any of it changes. A component that needs a part
// of it, or that is rendered many times (a task, a tool call), reads it
// with useChatSelector instead.
export function useChat() {
  const context = useContext(ChatContext);
  if (!context) {
    throw new Error("useChat must be used within a ChatProvider");
  }
  return context;
}

// useChatSelector returns selector's part of the chat's state; the
// component renders again only when that part changes (isEqual, by default
// the same object or value). Actions keep their identity, so selecting them
// doesn't render again.
export function useChatSelector<S>(selector: (chat: ChatContextValue) => S, isEqual?: (a: S, b: S) => boolean): S {
  const store = useContext(ChatStoreContext);
  if (!store) {
    throw new Error("useChatSelector must be used within a ChatProvider");
  }
  return useStoreSelector(store, selector, isEqual);
}
