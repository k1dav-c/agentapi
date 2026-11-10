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
  useReducer,
} from "react";
import {toast} from "sonner";
import {getErrorMessage} from "@/lib/error-utils";
import {getDocumentTitle} from "@/lib/document-title";
import {downloadFile} from "@/lib/download";
import {clearSession} from "@/lib/session-cache";
import {AuthDialog} from "@/components/auth-dialog";
import {
  createChatAPI,
  httpURL,
  type AgentSettings,
  type ChatAPI,
  type AgentUsage,
  type CostReport,
  type LimitStop,
  type RetryWait,
  type SendResult,
} from "@/lib/chat-api";
import {features, type Features} from "@/lib/features";
import type {AgentType} from "@/lib/agents";
import {initialAgentStatus, update, type AgentStatus, type ServerStatus} from "@/lib/agent-status";
import {
  canStop,
  type Capabilities,
  type DispatchContext,
  type DispatchMode,
} from "@/lib/dispatch";
import {
  pendingRequests,
  type NativeRequest,
  type RequestResponse,
} from "@/lib/native-requests";
import type {LoginRequired} from "@/lib/login";
import type {ConnectionStatus} from "@/lib/event-source";
import {getAgentState, waitingFor, type AgentStateKind, type TokenTotals, type WaitingFor} from "@/lib/session-status";
import {SyncTracker} from "@/lib/session-sync";
import {resolveAgentAPIUrl} from "@/lib/agentapi-url";
import {lastUserMessage, type DraftMessage, type Message, type RichMessage} from "@/lib/conversation";
import {conversationReducer, initialConversation} from "@/lib/conversation-state";
import type {Timeline, ToolCall} from "@/lib/task-timeline";
import type {QueuedMessage} from "@/lib/queue";
import {steeredMessages, tasksByMessage, type ServerTask} from "@/lib/server-tasks";
import {earlierTasks, HISTORY_PAGE_TASKS} from "@/lib/history";
import {useDrafts} from "./use-drafts";
import {useEventStream} from "./use-event-stream";
import {useQueue} from "./use-queue";
import {useSessionCache} from "./use-session-cache";
import {useSessionCost} from "./use-session-cost";
import {useTranscript} from "./use-transcript";
import {Store, useStoreSelector} from "@/lib/store";
import {useLatest, useStableCallback} from "@/lib/use-stable-callback";

export type {DraftMessage, Message, RichContentBlock, RichMessage, Usage} from "@/lib/conversation";

export type {FileUploadResponse, SendResult} from "@/lib/chat-api";
export type {QueuedMessage} from "@/lib/queue";

// The API calls components make as they are (lib/chat-api.ts).
const API_CALLS = [
  // GET /settings: the settings and the values they can take.
  "getSettings",
  // POST /compact (agents with the compact capability).
  "compact",
  // Continuing the turn a usage limit stopped, now or once the limit
  // resets; cancelling a scheduled continuation; dismissing the stop,
  // which lets the queued tasks go.
  "continueAfterLimit",
  "cancelContinue",
  "dismissLimitStop",
  // Trying a turn that failed on an upstream error again now, or not at
  // all, which lets the queued tasks go.
  "retryNow",
  "cancelRetry",
  // The agent's sessions in the working directory (features.sessionHistory).
  "listSessions",
  // The subagents the agent ran in its session, and what one did
  // (features.subagents).
  "listSubagents",
  "getSubagent",
  // Typing into and resizing the shell terminal (GET /terminal).
  "terminalInput",
  "terminalResize",
  // POST /tasks/seen: the user saw the conversation (SeenMarker).
  "markSeen",
  // GET, PUT /webhook and POST /webhook/test: pushing events to a receiver.
  "getWebhook",
  "updateWebhook",
  "testWebhook",
  // A file in the agent's working directory and its uncommitted changes,
  // for the file viewer (GET /files/content, GET /files/diff); the files
  // whose path ends with one the agent named that isn't there (GET
  // /files/find).
  "getFileContent",
  "getFileDiff",
  "getFilePreview",
  "findFile",
  "fileImageURL",
  "uploadFiles",
] as const;

// What components do: the chat's actions, and the API calls they make as
// they are. The value never changes (see useChatActions).
interface ChatActions extends Pick<ChatAPI, (typeof API_CALLS)[number]> {
  // PUT /settings: changes the settings set in change.
  updateSettings: (change: AgentSettings) => ReturnType<ChatAPI["updateSettings"]>;
  // Opens one of the agent's sessions (or a copy of it) in place of this
  // conversation.
  resumeSession: (id: string, fork: boolean) => Promise<void>;
  // Checks the login again now (POST /login/retry).
  retryLogin: () => Promise<void>;
  respondToRequest: (id: string, response: RequestResponse) => Promise<NativeRequest>;
  // Stops what the agent is doing now (POST /interrupt); see
  // ChatState.canStop. It reports
  // a failure itself and resolves to false.
  stop: () => Promise<boolean>;
  steerQueuedMessage: (id: number) => Promise<void>;
  reorderQueue: (ids: number[]) => Promise<void>;
  sendMessage: (message: string, mode?: DispatchMode) => Promise<SendResult>;
  retryFailedMessage: (clientId: string) => Promise<boolean>;
  dismissFailedMessage: (clientId: string) => void;
  updateQueuedMessage: (id: number, content: string) => Promise<void>;
  deleteQueuedMessage: (id: number) => Promise<void>;
  // Reconnects right away, as the user asked: also asks for the API token
  // again if the user closed the sign-in dialog.
  reconnectNow: () => void;
  downloadSession: () => Promise<void>;
  deleteMessages: () => Promise<void>;
  // Reads the page of tasks before the earliest the chat holds, or all of
  // them with all (lib/history.ts); a failure is reported and resolves.
  loadEarlier: (all?: boolean) => Promise<void>;
}

// The chat's state. Components read the parts they need with
// useChatSelector.
export interface ChatState {
  messages: (Message | DraftMessage)[];
  richMessages: RichMessage[];
  // What the transcript shows: the tasks, and the tool calls in the order
  // they started (TimelineBuilder).
  timeline: Timeline;
  toolCalls: ToolCall[];
  // The tokens the session used, and the task the agent works on (the last
  // one sent).
  tokenTotals: TokenTotals;
  taskTokens: TokenTotals;
  // What the agent does in that task ("$ go test ./...", "Thinking · …";
  // "" when it waits on nothing), and the last tool it used there.
  taskActivity: string;
  latestTool: string | null;
  // How many subagents the agent ran, and how many still run.
  subagents: {total: number; running: number};
  loading: boolean;
  serverStatus: ServerStatus;
  connectionStatus: ConnectionStatus;
  queuedMessages: QueuedMessage[];
  // Optional operations of the agent; null until it reports them.
  capabilities: Capabilities | null;
  // What the chat offers for the agent (lib/features).
  features: Features;
  // Number of requests the agent waits on (native transport), counted from
  // requests so that both always agree.
  pendingRequestCount: number;
  // Set while the agent waits for someone to log it in (native transport).
  loginRequired: LoginRequired | null;
  // The permission mode, model and effort of a native agent that can change
  // them (capabilities.settings); null otherwise.
  agentSettings: AgentSettings | null;
  // How full the context window is and the account's rate limits, as the
  // agent last reported them (native transport); null until it does.
  agentUsage: AgentUsage | null;
  // What the open session spent and its model calls, at list prices
  // (features.cost); null until read.
  sessionCost: CostReport | null;
  // Whether the server offers a shell terminal (native transport, GET
  // /terminal).
  terminalAvailable: boolean;
  // The turn a usage limit stopped, while it holds the queue (native
  // transport); null otherwise.
  limitStop: LimitStop | null;
  // The turn that failed on an upstream error and waits to be tried again,
  // while it holds the queue (native transport); null otherwise.
  retryWait: RetryWait | null;
  // The agent is starting (lifecycle "starting"): there is no turn yet, and
  // while it finds no login AgentAPI starts it again by itself.
  agentStarting: boolean;
  // What the agent is doing from the user's point of view ("Needs you",
  // "Working", ...), for every view that shows it, and while it needs you,
  // what for.
  agentState: AgentStateKind;
  waitingFor: WaitingFor;
  // The user's last message: the task the agent works on, or worked on
  // last.
  currentTask?: Message | DraftMessage;
  // What the composer may offer: steer, interrupt (see lib/dispatch).
  dispatch: DispatchContext;
  // Requests the agent made (native transport): pending ones and the most
  // recently closed, oldest first.
  requests: NativeRequest[];
  // The tasks as the server derives them, the earliest first; none from
  // servers that don't. And each by the user messages that belong to it
  // (tasksByMessage).
  serverTasks: ServerTask[];
  taskByMessage: ReadonlyMap<number, ServerTask>;
  // How many of the server's tasks came before those the chat holds
  // (lib/history.ts), and whether it is reading them.
  earlierTasks: number;
  loadingEarlier: boolean;
  // Whether stop can stop what the agent is doing now.
  canStop: boolean;
  reconnectAttempt: number;
  nextReconnectAt: number | null;
  storageScope: string;
  agentType: AgentType;
  // AgentAPI server version (e.g. "0.14.0"); empty until the first status
  // event or when talking to a server that predates version reporting.
  agentapiVersion: string;
  // What the agent calls the session (Claude Code's title, Codex's thread
  // name, Pi's /name); empty until it names it.
  sessionName: string;
  // The agent's own session ID (native transport): as the status says, or
  // GET /cost; empty until either does.
  agentSessionId: string;
  customTitle?: string;
  // The Coder workspace: the ?workspace= parameter, or what the server says
  // (GET /workspace).
  workspaceUrl?: string;
}

const ChatActionsContext = createContext<ChatActions | undefined>(undefined);
const ChatStoreContext = createContext<Store<ChatState> | undefined>(undefined);

const useAgentAPIUrl = (): string => {
  const searchParams = useSearchParams();
  const url = resolveAgentAPIUrl(searchParams.get("url"));
  if (!url) {
    throw new Error(
      "agentAPIUrl is not set. Please set the url query parameter to the URL of the AgentAPI or the NEXT_PUBLIC_BASE_PATH environment variable."
    );
  }
  return url;
};

export function ChatProvider({ children }: PropsWithChildren) {
  const agentAPIUrl = useAgentAPIUrl();
  // Another server is another chat: its state and actions start over.
  return (
    <ChatSession key={agentAPIUrl} agentAPIUrl={agentAPIUrl}>
      {children}
    </ChatSession>
  );
}

function ChatSession({ agentAPIUrl, children }: PropsWithChildren<{agentAPIUrl: string}>) {
  const searchParams = useSearchParams();
  const customTitle = searchParams.get("title") ?? undefined;
  const workspaceValue = searchParams.get("workspace");
  const workspaceParam = workspaceValue ? httpURL(workspaceValue, window.location.origin) : undefined;
  const [serverWorkspaceUrl, setServerWorkspaceUrl] = useState<string>();
  // The conversation: messages, rich messages, requests, the server's
  // tasks, and the point of the server's conversation it is at
  // (lib/conversation-state).
  const [conversation, dispatchConversation] = useReducer(conversationReducer, initialConversation);
  const {messages, richMessages, requests, tasks: serverTasks, synced} = conversation;
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
    sessionName,
    agentSessionId,
    capabilities,
    settings: agentSettings,
    usage: agentUsage,
    limitStop,
    retryWait,
    terminalAvailable,
    loginRequired,
    lifecycle,
  } = agentStatus;
  const [api] = useState(() => createChatAPI(agentAPIUrl));
  // Server conversation state the client holds: its epoch and the highest
  // change sequence applied. Sent as /events?since=&epoch= so a reconnect
  // only receives what changed.
  const syncRef = useRef(new SyncTracker());
  // The requests as last rendered, for refreshRequests.
  const requestsRef = useLatest(requests);
  const {queuedMessages, refreshQueue, applyQueue, queueChanged, queueMayHaveSent, updateQueuedMessage, deleteQueuedMessage, steerQueuedMessage, reorderQueue} =
    useQueue(api, agentapiVersion);
  // Reads the requests from the REST API; see mergeListedRequests.
  const refreshRequests = async () => {
    const heldWhenAsked = requestsRef.current;
    const listed = await api.getRequests();
    dispatchConversation({type: "requestsListed", listed, heldWhenAsked});
  };
  // A connection that resumes gets what changed since, but the server lets
  // go of closed requests without a word: a request still held as pending
  // may be gone. Once the replay is applied, the requests are listed again.
  const onReplayed = useStableCallback(() => {
    if (pendingRequests(requestsRef.current).length > 0) void refreshRequests().catch(() => undefined);
  });
  const {connectionStatus, reconnectAttempt, nextReconnectAt, reconnectByUser} = useEventStream({
    agentAPIUrl,
    syncRef,
    dispatch: dispatchConversation,
    updateAgentStatus,
    agentStatusRef,
    refreshQueue,
    applyQueue,
    queueMayHaveSent,
    onReplayed,
  });
  useSessionCache({agentAPIUrl, synced, messages, richMessages, requests, tasks: serverTasks});
  const pendingRequestCount = useMemo(() => pendingRequests(requests).length, [requests]);
  const agentStarting = lifecycle === "starting";
  const needs = {pendingRequestCount, loginRequired: loginRequired !== null, limitStop: limitStop !== null};
  const agentState = getAgentState({serverStatus, connectionStatus, retryWait: retryWait !== null, ...needs});
  const dispatch = useMemo<DispatchContext>(
    () => ({capabilities: agentStarting ? null : capabilities, serverStatus, pendingRequestCount}),
    [agentStarting, capabilities, pendingRequestCount, serverStatus],
  );
  const chatFeatures = useMemo(
    () => features(capabilities, terminalAvailable, agentType),
    [agentType, capabilities, terminalAvailable],
  );
  const sessionCost = useSessionCost(api, chatFeatures.cost, serverStatus, lifecycle !== "starting" && lifecycle !== "restarting");
  const currentTask = useMemo(() => lastUserMessage(messages), [messages]);
  const steered = useMemo(() => steeredMessages(serverTasks), [serverTasks]);
  const transcript = useTranscript(messages, richMessages, steered);
  const taskByMessage = useMemo(() => tasksByMessage(serverTasks), [serverTasks]);

  // Rendered as <title> rather than set on document.title from an effect:
  // Next re-applies the layout's static title after hydration, which
  // overwrote the state until it changed again.
  const documentTitle = getDocumentTitle({agentState, task: currentTask?.content, customTitle});

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

  const {loading, sendMessage, retryFailedMessage, dismissFailedMessage} = useDrafts({agentAPIUrl, api, messages, dispatch: dispatchConversation, queueChanged});

  const respondToRequest = async (id: string, response: RequestResponse) => {
    try {
      const state = await api.respondRequest(id, response);
      dispatchConversation({type: "requestAnswered", request: state});
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
      await api.interrupt();
      toast.info("Stopping the agent");
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

  // The earlier tasks: a page at a time, the one before the earliest the
  // chat holds; with all, until there are none. A page that doesn't end
  // where the chat's tasks start (a full replay replaced them meanwhile)
  // is dropped (prependPage).
  const earlier = earlierTasks(serverTasks);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const loadingEarlierRef = useRef(false);
  const serverTasksRef = useLatest(serverTasks);
  const loadEarlier = async (all = false) => {
    if (loadingEarlierRef.current) return;
    let before = serverTasksRef.current[0]?.id;
    if (before === undefined || earlierTasks(serverTasksRef.current) === 0) return;
    loadingEarlierRef.current = true;
    setLoadingEarlier(true);
    try {
      for (;;) {
        const page = await api.getHistoryPage(before, HISTORY_PAGE_TASKS);
        dispatchConversation({type: "earlierLoaded", page});
        const first = page.tasks[0]?.id;
        if (!all || !page.hasMore || first === undefined) break;
        before = first;
      }
    } catch (error) {
      toast.error("Failed to load earlier tasks", {description: getErrorMessage(error)});
    } finally {
      loadingEarlierRef.current = false;
      setLoadingEarlier(false);
    }
  };

  const downloadSession = async () => {
    try {
      const jsonl = await api.exportTimeline();
      const timestamp = new Date().toISOString().replaceAll(":", "-");
      downloadFile(`agentapi-session-${timestamp}.jsonl`, jsonl, "application/x-ndjson");
      toast.success("Session JSONL downloaded");
    } catch (error) {
      toast.error("Session download failed", {
        description: getErrorMessage(error),
      });
      throw error;
    }
  };

  // Starts the conversation over after change (DELETE /messages, resuming
  // a session). The server's reset arrives as a session_sync of a new
  // epoch, with what the new session sent since (a restart can take
  // minutes): clearing then would drop both. Only a server that doesn't
  // send it needs clearing here.
  const startOver = async (change: () => Promise<unknown>) => {
    const epoch = syncRef.current.epoch;
    await change();
    if (syncRef.current.epoch !== epoch) return;
    syncRef.current.reset();
    void clearSession(agentAPIUrl);
    dispatchConversation({type: "reset"});
  };

  // The actions keep their identity from render to render, and each calls
  // what the latest render made of it: the object is made once, and a
  // component that takes it never renders again for it.
  const callbacks = {
    sendMessage: useStableCallback(sendMessage),
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
    reconnectNow: useStableCallback(reconnectByUser),
    downloadSession: useStableCallback(downloadSession),
    loadEarlier: useStableCallback(loadEarlier),
    deleteMessages: useStableCallback(() => startOver(() => api.deleteMessages())),
    resumeSession: useStableCallback((id: string, fork: boolean) => startOver(() => api.resumeSession(id, fork))),
  };
  const [actions] = useState<ChatActions>(() => ({
    ...(Object.fromEntries(API_CALLS.map((name) => [name, api[name]])) as Pick<ChatAPI, (typeof API_CALLS)[number]>),
    ...callbacks,
  }));
  const state: ChatState = {
    ...transcript,
    messages,
    richMessages,
    loading,
    capabilities,
    features: chatFeatures,
    pendingRequestCount,
    loginRequired,
    agentSettings,
    agentUsage,
    sessionCost,
    terminalAvailable,
    limitStop,
    retryWait,
    agentStarting,
    agentState,
    waitingFor: waitingFor(agentState, needs),
    currentTask,
    dispatch,
    requests,
    serverTasks,
    taskByMessage,
    earlierTasks: earlier,
    loadingEarlier,
    canStop: stoppable,
    serverStatus,
    connectionStatus,
    queuedMessages,
    reconnectAttempt,
    nextReconnectAt,
    storageScope: agentAPIUrl,
    agentType,
    agentapiVersion,
    sessionName,
    agentSessionId: agentSessionId || sessionCost?.agent_session_id || "",
    customTitle,
    workspaceUrl: workspaceParam ?? serverWorkspaceUrl,
  };
  // Components read a part of the state with useChatSelector and render
  // again only when that part changes; the state is handed over once this
  // render is committed.
  const storeRef = useRef<Store<ChatState> | null>(null);
  storeRef.current ??= new Store(state);
  const store = storeRef.current;
  useLayoutEffect(() => store.set(state));

  return (
    <ChatStoreContext.Provider value={store}>
      <ChatActionsContext.Provider value={actions}>
        <title>{documentTitle}</title>
        {children}
        <AuthDialog />
      </ChatActionsContext.Provider>
    </ChatStoreContext.Provider>
  );
}

// useChatActions returns what components can do: the chat's actions and
// its API calls. The value never changes, so it never renders a component
// again.
export function useChatActions(): ChatActions {
  const actions = useContext(ChatActionsContext);
  if (!actions) {
    throw new Error("useChatActions must be used within a ChatProvider");
  }
  return actions;
}

// useChatSelector returns selector's part of the chat's state; the
// component renders again only when that part changes (isEqual, by default
// the same object or value). Select the smallest part that does: a
// component that selects the messages renders again as they stream.
export function useChatSelector<S>(selector: (chat: ChatState) => S, isEqual?: (a: S, b: S) => boolean): S {
  const store = useContext(ChatStoreContext);
  if (!store) {
    throw new Error("useChatSelector must be used within a ChatProvider");
  }
  return useStoreSelector(store, selector, isEqual);
}
