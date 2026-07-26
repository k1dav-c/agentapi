"use client";

import { useSearchParams } from "next/navigation";
import {
  useState,
  useEffect,
  useRef,
  useCallback,
  createContext,
  PropsWithChildren,
  useContext,
} from "react";
import {toast} from "sonner";
import {getErrorMessage} from "@/lib/error-utils";
import {getDocumentTitle} from "@/lib/document-title";
import {getReconnectDelay} from "@/lib/reconnect";
import {parseFailedMessages} from "@/lib/failed-messages";

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

export interface RichMessage {
  message_id: string;
  role: string;
  content: RichContentBlock[];
  timestamp: string;
}

interface StatusChangeEvent {
  status: string;
  agent_type: string;
}

interface ErrorEventData {
  message: string;
  level: string;
  time: string;
}

interface APIErrorDetail {
  location: string;
  message: string;
  value: null | string | number | boolean | object;
}

interface APIErrorModel {
  $schema: string;
  detail: string;
  errors: APIErrorDetail[];
  instance: string;
  status: number;
  title: string;
  type: string;
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

interface UploadOptions {
  signal?: AbortSignal;
  onProgress?: (progress: number) => void;
}

export interface QueuedMessage {
  id: number;
  content: string;
  time: string;
}

export interface BackgroundTask {
  id: string;
  name: string;
  status: "running" | "completed" | "failed" | "unknown";
  agent_type: string;
  tool_use_id: string;
  output_path?: string;
  started_at: string;
  updated_at: string;
}

export type AgentType = "claude" | "goose" | "aider" | "gemini" | "amp" | "codex" | "cursor" | "cursor-agent" | "copilot" | "auggie" | "amazonq" | "opencode" | "custom" | "unknown";

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
  custom: { displayName: "Custom"}
}

interface ChatContextValue {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  backgroundTasks: BackgroundTask[];
  loading: boolean;
  serverStatus: ServerStatus;
  connectionStatus: ConnectionStatus;
  queuedMessages: QueuedMessage[];
  sendMessage: (message: string, type?: MessageType) => Promise<SendResult>;
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
  refreshBackgroundTasks: () => Promise<void>;
  getBackgroundTaskOutput: (id: string) => Promise<{
    content: string;
    path: string;
    size: number;
    truncated: boolean;
  }>;
  storageScope: string;
  agentType: AgentType;
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
  const [messages, setMessages] = useState<(Message | DraftMessage)[]>([]);
  const [richMessages, setRichMessages] = useState<RichMessage[]>([]);
  const [backgroundTasks, setBackgroundTasks] = useState<BackgroundTask[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [serverStatus, setServerStatus] = useState<ServerStatus>("unknown");
  const [connectionStatus, setConnectionStatus] =
    useState<ConnectionStatus>("reconnecting");
  const [queuedMessages, setQueuedMessages] = useState<QueuedMessage[]>([]);
  const [agentType, setAgentType] = useState<AgentType>("custom");
  const eventSourceRef = useRef<EventSource | null>(null);
  const lastEventAtRef = useRef(Date.now());
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef(0);
  const [reconnectAttempt, setReconnectAttempt] = useState(0);
  const [nextReconnectAt, setNextReconnectAt] = useState<number | null>(null);
  const [reconnectNonce, setReconnectNonce] = useState(0);
  const [failedMessagesHydrated, setFailedMessagesHydrated] = useState(false);
  const agentAPIUrl = useAgentAPIUrl();
  const failedMessagesStorageKey = `agentapi.chat.failed-messages:${agentAPIUrl}`;

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
  const refreshQueue = useCallback(async () => {
    try {
      const response = await fetch(`${agentAPIUrl}/queue`);
      if (!response.ok) return;
      const data = await response.json() as {messages: QueuedMessage[]};
      setQueuedMessages(data.messages ?? []);
    } catch {
      // The connection status handler reports connectivity failures.
    }
  }, [agentAPIUrl]);
  const refreshBackgroundTasks = useCallback(async () => {
    try {
      const response = await fetch(`${agentAPIUrl}/background-tasks`);
      if (!response.ok) return;
      const data = await response.json() as {tasks?: BackgroundTask[]};
      setBackgroundTasks(data.tasks ?? []);
    } catch {
      // The primary connection state handles connectivity feedback.
    }
  }, [agentAPIUrl]);
  const getBackgroundTaskOutput = useCallback(async (id: string) => {
    const response = await fetch(
      `${agentAPIUrl}/background-tasks/${encodeURIComponent(id)}/output`,
    );
    if (!response.ok) throw new Error("Background task output is unavailable");
    return await response.json() as {
      content: string;
      path: string;
      size: number;
      truncated: boolean;
    };
  }, [agentAPIUrl]);

  useEffect(() => {
    void refreshBackgroundTasks();
  }, [refreshBackgroundTasks]);

  const hasRunningBackgroundTasks = backgroundTasks.some(
    (task) => task.status === "running",
  );
  useEffect(() => {
    if (!hasRunningBackgroundTasks) return;
    const timer = window.setInterval(() => void refreshBackgroundTasks(), 3000);
    return () => window.clearInterval(timer);
  }, [hasRunningBackgroundTasks, refreshBackgroundTasks]);
  const currentTask = [...messages]
    .reverse()
    .find((message) => message.role === "user")
    ?.content;

  useEffect(() => {
    document.title = getDocumentTitle({
      connectionStatus,
      serverStatus,
      task: currentTask,
    });
  }, [connectionStatus, currentTask, serverStatus]);

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

      const eventSource = new EventSource(`${agentAPIUrl}/events`);
      eventSourceRef.current = eventSource;

      // Server-sent keep-alive; only used to detect dead connections.
      eventSource.addEventListener("heartbeat", () => {
        lastEventAtRef.current = Date.now();
      });

      // Handle message updates
      eventSource.addEventListener("message_update", (event) => {
        lastEventAtRef.current = Date.now();
        const data: MessageUpdateEvent = JSON.parse(event.data);

        setMessages((prevMessages) => {
          // Clean up draft messages
          const updatedMessages = [...prevMessages].filter(
            (message) =>
              !isDraftMessage(message) ||
              (message as DraftMessage).deliveryStatus === "failed",
          );

          // Check if message with this ID already exists
          const existingIndex = updatedMessages.findIndex(
            (m) => m.id === data.id
          );

          if (existingIndex !== -1) {
            // Update existing message
            updatedMessages[existingIndex] = {
              role: data.role,
              content: data.message,
              id: data.id,
              time: data.time,
            };
            return updatedMessages;
          } else {
            // Add new message
            return [
              ...updatedMessages,
              {
                role: data.role,
                content: data.message,
                id: data.id,
                time: data.time,
              },
            ];
          }
        });
      });

      eventSource.addEventListener("rich_message_update", (event) => {
        const data: RichMessage = JSON.parse(event.data);
        setRichMessages((previous) => {
          const existingIndex = previous.findIndex(
            (message) =>
              message.message_id === data.message_id &&
              message.role === data.role,
          );
          if (existingIndex === -1) return [...previous, data];

          const updated = [...previous];
          updated[existingIndex] = data;
          return updated;
        });
        if (
          data.content.some(
            (block) => block.type === "tool_use" || block.type === "tool_result",
          )
        ) {
          void refreshBackgroundTasks();
        }
      });

      // Handle status changes
      eventSource.addEventListener("status_change", (event) => {
        lastEventAtRef.current = Date.now();
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
        void refreshQueue();
        void refreshBackgroundTasks();
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

    // Initial setup
    setupEventSource();

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
  }, [agentAPIUrl, reconnectNonce, reconnectNow, refreshBackgroundTasks, refreshQueue]);

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
      const response = await fetch(`${agentAPIUrl}/message`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          content: content,
          type,
        }),
      });

      if (!response.ok) {
        const errorData = await response.json() as APIErrorModel;
        console.error("Failed to send message:", errorData);
        const detail = errorData.detail;
        const messages =
          "errors" in errorData
            ?
              errorData.errors.map((e: APIErrorDetail) => e.message).join(", ")
            : "";

        const fullDetail = `${detail}: ${messages}`;
        throw new Error(fullDetail);
      }
      const result = await response.json() as {
        ok?: boolean;
        queued?: boolean;
      };
      await refreshQueue();
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
        ok: result.ok === true,
        queued: result.queued === true,
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
  const uploadFiles = (
    formData: FormData,
    options: UploadOptions = {},
  ): Promise<FileUploadResponse> =>
    new Promise((resolve) => {
      const request = new XMLHttpRequest();
      request.open("POST", `${agentAPIUrl}/upload`);
      request.upload.onprogress = (event) => {
        if (event.lengthComputable) {
          options.onProgress?.(
            Math.min(100, Math.round((event.loaded / event.total) * 100)),
          );
        }
      };
      request.onload = () => {
        try {
          const data = JSON.parse(request.responseText) as
            | FileUploadResponse
            | APIErrorModel;
          if (request.status >= 200 && request.status < 300) {
            options.onProgress?.(100);
            resolve(data as FileUploadResponse);
            return;
          }
          const error =
            "detail" in data ? data.detail : "The upload was rejected.";
          resolve({ok: false, error});
        } catch {
          resolve({ok: false, error: "The server returned an invalid response."});
        }
      };
      request.onerror = () =>
        resolve({ok: false, error: "The upload connection failed."});
      request.onabort = () =>
        resolve({ok: false, error: "Upload cancelled."});
      options.signal?.addEventListener("abort", () => request.abort(), {
        once: true,
      });
      request.send(formData);
    });

  const updateQueuedMessage = async (id: number, content: string) => {
    const response = await fetch(`${agentAPIUrl}/queue/${id}`, {
      method: "PUT",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({content}),
    });
    if (!response.ok) {
      throw new Error("Failed to update queued message");
    }
    await refreshQueue();
  };

  const deleteQueuedMessage = async (id: number) => {
    const response = await fetch(`${agentAPIUrl}/queue/${id}`, {
      method: "DELETE",
    });
    if (!response.ok) {
      throw new Error("Failed to delete queued message");
    }
    await refreshQueue();
  };

  const downloadSession = async () => {
    try {
      const response = await fetch(`${agentAPIUrl}/timeline`);
      if (!response.ok) {
        throw new Error("Failed to export the current session");
      }
      const data = await response.json();
      const events = data.events;
      if (!Array.isArray(events)) {
        throw new Error("The server returned an invalid timeline response");
      }
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
        backgroundTasks,
        loading,
        sendMessage,
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
        refreshBackgroundTasks,
        getBackgroundTaskOutput,
        storageScope: agentAPIUrl,
        agentType,
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
