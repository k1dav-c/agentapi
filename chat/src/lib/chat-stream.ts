// The chat's /events stream: it opens from where the chat's state is (the
// tracker's epoch and watermark), collects the events and hands them on in
// batches (EVENT_FLUSH_MS), a replay at once when it is complete
// (SyncTracker), and keeps the connection open (EventSourceConnection).
// use-event-stream.ts applies what it hands on to the React state.

import {parseTransport, type Transport} from "./dispatch";
import {
  messageFromEvent,
  queueFromEvent,
  requestFromEvent,
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
} from "./events";
import {
  browserEnvironment,
  EventSourceConnection,
  type Clock,
  type ConnectionEnvironment,
  type ConnectionState,
  type StreamListeners,
} from "./event-source";
import {PendingEvents, type EventBatch} from "./pending-events";
import type {SyncTracker} from "./session-sync";

// How long incoming conversation events are collected before they are
// applied together. A reload replays the whole session as one event per
// message; applying them one by one re-rendered the transcript each time.
export const EVENT_FLUSH_MS = 32;
// While a reconnect replays state, updates are held until the replay is
// complete and applied at once; a very long replay still shows progress
// this often.
export const REPLAY_FLUSH_MAX_MS = 1500;
// The server sends a heartbeat every 15s. Nothing (heartbeat or otherwise)
// for this long means the connection is dead, even when the EventSource
// still reports itself open.
export const STALE_CONNECTION_MS = 45_000;

export interface ChatStreamHandlers {
  // Applies a batch of updates, with the point of the conversation they
  // bring the state to.
  apply(batch: EventBatch): void;
  // The replay of a connection that resumed was applied: it doesn't tell
  // of what the server dropped meanwhile (closed requests).
  replayed(): void;
  statusChange(event: StatusChangeEvent): void;
  transport(transport: Transport): void;
  // The status didn't say the transport (an older server): GET /status
  // does. Asked once per connection.
  askTransport(): void;
  agentError(event: AgentErrorEvent): void;
  // The stream opened: the queue is read on every connection.
  opened(): void;
  // A connection failed: EventSource hides a 401, so the session is checked.
  failed(): void;
  connection(state: ConnectionState): void;
}

export class ChatStream {
  private readonly pending: PendingEvents;
  private readonly connection: EventSourceConnection;
  private readonly clock: Clock;
  private flushTimer: unknown;
  // The replay of a connection that resumed (not a full one) is on its way,
  // and was applied, for replayed.
  private partialReplay = false;
  private replayedPending = false;

  constructor(
    agentAPIUrl: string,
    private readonly sync: SyncTracker,
    private readonly handlers: ChatStreamHandlers,
    {withCredentials = false, env = browserEnvironment()}: {withCredentials?: boolean; env?: ConnectionEnvironment} = {},
  ) {
    this.pending = new PendingEvents(sync);
    this.clock = env.clock;
    this.connection = new EventSourceConnection(
      {
        url: () => {
          const {epoch, watermark} = this.sync;
          return epoch
            ? `${agentAPIUrl}/events?sync=1&since=${watermark}&epoch=${encodeURIComponent(epoch)}`
            : `${agentAPIUrl}/events?sync=1`;
        },
        withCredentials,
        staleAfter: STALE_CONNECTION_MS,
        listeners: () => this.listeners(),
        onOpen: () => handlers.opened(),
        onError: () => handlers.failed(),
        onChange: (state) => handlers.connection(state),
      },
      env,
    );
  }

  start() {
    this.connection.start();
  }

  // Stops the stream; what was received and not applied yet is applied.
  stop() {
    this.connection.stop();
    if (this.flushTimer !== undefined) this.flush();
  }

  reconnectNow() {
    this.connection.reconnectNow();
  }

  private flush = () => {
    if (this.flushTimer !== undefined) this.clock.clearTimeout(this.flushTimer);
    this.flushTimer = undefined;
    this.handlers.apply(this.pending.take());
    if (this.replayedPending) {
      this.replayedPending = false;
      this.handlers.replayed();
    }
  };

  // Schedules applying the events received so far. An event that completes
  // the replay (see SyncTracker) applies the replay now, in one batch.
  private schedule(event?: MessageEvent<string>, immediate = false) {
    if (event && this.sync.noteEvent(event.lastEventId)) {
      this.replayedPending ||= this.partialReplay;
      this.partialReplay = false;
      if (this.flushTimer !== undefined) this.clock.clearTimeout(this.flushTimer);
      this.flushTimer = this.clock.setTimeout(this.flush, 0);
      return;
    }
    const replaying = this.sync.replaying;
    if (immediate && !replaying) {
      this.flush();
      return;
    }
    if (this.flushTimer !== undefined) return;
    this.flushTimer = this.clock.setTimeout(this.flush, replaying ? REPLAY_FLUSH_MAX_MS : EVENT_FLUSH_MS);
  }

  // The listeners of one connection.
  private listeners(): StreamListeners {
    const {sync, pending, handlers} = this;
    let askedTransport = false;
    // Servers that predate session_sync replay everything on every
    // connection without saying so; treat that replay as the full state.
    let synced = false;
    const assumeFullReplay = () => {
      if (synced) return;
      synced = true;
      sync.reset();
      pending.replaceAll();
    };
    return {
      session_sync: (event) => {
        synced = true;
        const full = sync.begin(JSON.parse(event.data) as SessionSyncEvent, event.lastEventId);
        // Everything that follows is the full state: it replaces what the
        // cache or an earlier connection left. Otherwise it is what changed
        // since, which says nothing of the closed requests the server let
        // go of in the meantime: once it is applied, replayed.
        if (full) pending.replaceAll();
        this.partialReplay = !full && sync.replaying;
        this.replayedPending = !full && !sync.replaying;
        if (full || !sync.replaying) this.schedule();
      },
      // Keeps the connection alive; the replay's last event may be one.
      heartbeat: (event) => {
        if (sync.replaying) this.schedule(event);
      },
      message_update: (event) => {
        assumeFullReplay();
        pending.addMessage(messageFromEvent(JSON.parse(event.data) as MessageUpdateEvent));
        this.schedule(event);
      },
      rich_message_update: (event) => {
        assumeFullReplay();
        pending.addRichMessage(richMessageFromEvent(JSON.parse(event.data) as RichMessageUpdateEvent));
        this.schedule(event);
      },
      // A request the agent waits on (native transport) opened or changed.
      // Requests are part of the replay, like messages. Outside a replay
      // one applies at once: the status_change the server sends right
      // after it (now "stable", waiting) must not show before it.
      request_update: (event) => {
        assumeFullReplay();
        pending.addRequest(requestFromEvent(JSON.parse(event.data) as RequestUpdateEvent));
        this.schedule(event, true);
      },
      // A task the server derives started or changed (sync clients). Tasks
      // are part of the replay, like requests, and come last in it.
      task_update: (event) => {
        pending.addTask(taskFromEvent(JSON.parse(event.data) as TaskUpdateEvent));
        this.schedule(event);
      },
      // The message queue changed (sync clients): the whole queue, applied
      // with the messages it sent. The replay holds the latest.
      queue_update: (event) => {
        const data = JSON.parse(event.data) as QueueUpdateEvent;
        pending.setQueue(queueFromEvent(data), data.seq);
        this.schedule(event);
      },
      status_change: (event) => {
        assumeFullReplay();
        const data = JSON.parse(event.data) as StatusChangeEvent;
        // The replay's last event may be its status.
        if (sync.replaying) this.schedule(event);
        handlers.statusChange(data);
        // The transport decides whether the terminal (TTY mode, raw keys)
        // exists at all.
        const reported = parseTransport(data.transport);
        if (reported) {
          handlers.transport(reported);
        } else if (!askedTransport) {
          askedTransport = true;
          handlers.askTransport();
        }
      },
      agent_error: (event) => {
        if (sync.replaying) this.schedule(event);
        try {
          handlers.agentError(JSON.parse(event.data) as AgentErrorEvent);
        } catch (error) {
          console.error("Failed to parse agent_error event data:", error);
        }
      },
    };
  }
}
