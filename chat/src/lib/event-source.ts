// A server-sent event stream kept open. The browser's EventSource opens a
// stream again by itself only after some failures: a response that isn't
// a 200 (a proxy's 502, a 401) closes it for good, and a connection that
// died without a FIN (sleep, a phone changing networks) still looks open.
// EventSourceConnection closes the stream on any failure and opens it
// again: at once, without showing, when a stream that was open ended (see
// reconnectAtOnce), else after a backoff. It also opens it again when the
// stream goes quiet for longer than its heartbeats allow, when the browser
// comes back online, and when the tab comes back with the stream closed or
// quiet. The chat's /events, the hub's /api/events and the terminals use
// it.

import {getReconnectDelay, reconnectAtOnce} from "./reconnect";

export type ConnectionStatus = "connected" | "reconnecting" | "offline";

export interface ConnectionState {
  status: ConnectionStatus;
  // The failed attempts since the stream was last open, and when the next
  // one is (null when none waits).
  attempt: number;
  nextAttemptAt: number | null;
}

export const initialConnectionState: ConnectionState = {status: "reconnecting", attempt: 0, nextAttemptAt: null};

// The parts of EventSource used here.
export interface EventSourceLike {
  readonly readyState: number;
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void;
  close(): void;
}

export interface Clock {
  now(): number;
  setTimeout(callback: () => void, ms: number): unknown;
  clearTimeout(timer: unknown): void;
  setInterval(callback: () => void, ms: number): unknown;
  clearInterval(timer: unknown): void;
}

// What a connection uses of the browser; tests give it fakes.
export interface ConnectionEnvironment {
  EventSource: new (url: string, init?: EventSourceInit) => EventSourceLike;
  clock: Clock;
  // online, offline and focus.
  window: Pick<EventTarget, "addEventListener" | "removeEventListener">;
  // visibilitychange.
  document: Pick<EventTarget, "addEventListener" | "removeEventListener"> & {readonly visibilityState: string};
  navigator: {readonly onLine: boolean};
}

export function browserEnvironment(): ConnectionEnvironment {
  return {
    EventSource: globalThis.EventSource,
    clock: {
      now: () => Date.now(),
      setTimeout: (callback, ms) => globalThis.setTimeout(callback, ms),
      clearTimeout: (timer) => globalThis.clearTimeout(timer as ReturnType<typeof setTimeout>),
      setInterval: (callback, ms) => globalThis.setInterval(callback, ms),
      clearInterval: (timer) => globalThis.clearInterval(timer as ReturnType<typeof setInterval>),
    },
    window,
    document,
    navigator,
  };
}

export type StreamListeners = Record<string, (event: MessageEvent<string>) => void>;

export interface ConnectionOptions {
  // The stream's URL, read for every connection (the chat's says where it
  // resumes); null doesn't connect.
  url: () => string | null;
  withCredentials?: boolean;
  // The listeners of one connection by event type, made for each: they may
  // keep what they need of it.
  listeners: () => StreamListeners;
  // How long the stream may go without an event, heartbeats included,
  // before it is taken for dead. Unset for a stream without heartbeats.
  staleAfter?: number;
  onOpen?: () => void;
  // A connection failed, and the next waits for the backoff. EventSource
  // hides the status code: this is the place to ask whether the server
  // wants a sign-in.
  onError?: () => void;
  onChange?: (state: ConnectionState) => void;
}

// How long a reconnect that doesn't show may take before it shows.
export const QUIET_RECONNECT_MS = 3000;
// How often the watchdog looks for a stream gone quiet, at most.
const WATCHDOG_MS = 5000;
const CLOSED = 2;

export class EventSourceConnection {
  private readonly env: ConnectionEnvironment;
  private source: EventSourceLike | null = null;
  private opened = false;
  private lastEventAt = 0;
  private retryTimer: unknown;
  private quietTimer: unknown;
  private watchdog: unknown;
  // When the latest reconnects that didn't show were; see reconnectAtOnce.
  private quickReconnects: number[] = [];
  private started = false;
  private stopped = false;
  private state = initialConnectionState;

  constructor(private readonly options: ConnectionOptions, env?: ConnectionEnvironment) {
    this.env = env ?? browserEnvironment();
  }

  get current(): ConnectionState {
    return this.state;
  }

  start() {
    if (this.started || this.stopped) return;
    this.started = true;
    this.env.window.addEventListener("online", this.handleOnline);
    this.env.window.addEventListener("offline", this.handleOffline);
    this.env.window.addEventListener("focus", this.handleVisible);
    this.env.document.addEventListener("visibilitychange", this.handleVisible);
    const {staleAfter} = this.options;
    if (staleAfter !== undefined) this.watchdog = this.env.clock.setInterval(this.checkStale, Math.min(WATCHDOG_MS, staleAfter));
    this.connect();
  }

  stop() {
    if (this.stopped) return;
    this.stopped = true;
    this.env.window.removeEventListener("online", this.handleOnline);
    this.env.window.removeEventListener("offline", this.handleOffline);
    this.env.window.removeEventListener("focus", this.handleVisible);
    this.env.document.removeEventListener("visibilitychange", this.handleVisible);
    if (this.watchdog !== undefined) this.env.clock.clearInterval(this.watchdog);
    this.clearRetry();
    this.close();
  }

  // Opens the stream again now, with the backoff from the start: the user
  // asked, or just signed in.
  reconnectNow() {
    if (!this.started || this.stopped) return;
    this.clearRetry();
    this.set({status: "reconnecting", attempt: 0, nextAttemptAt: null});
    this.connect();
  }

  // With quiet, the status stays as it is for QUIET_RECONNECT_MS.
  private connect(quiet = false) {
    this.close();
    const url = this.options.url();
    if (url === null) return;
    const {clock} = this.env;
    const source = new this.env.EventSource(url, {withCredentials: this.options.withCredentials ?? false});
    this.source = source;
    this.opened = false;
    this.lastEventAt = clock.now();
    if (quiet) {
      this.quietTimer = clock.setTimeout(() => {
        if (this.source === source && !this.opened) this.set({status: "reconnecting"});
      }, QUIET_RECONNECT_MS);
    }
    const listeners = this.options.listeners();
    for (const [type, listener] of Object.entries(listeners)) {
      source.addEventListener(type, (event) => {
        if (this.source !== source) return;
        this.lastEventAt = clock.now();
        listener(event);
      });
    }
    if (!listeners.heartbeat) {
      source.addEventListener("heartbeat", () => {
        if (this.source === source) this.lastEventAt = clock.now();
      });
    }
    source.onopen = () => {
      if (this.source !== source) return;
      this.opened = true;
      this.clearQuiet();
      this.lastEventAt = clock.now();
      this.set({status: "connected", attempt: 0, nextAttemptAt: null});
      this.options.onOpen?.();
    };
    source.onerror = () => {
      if (this.source === source) this.fail();
    };
  }

  // The stream ended, failed or went quiet: it is closed, and opened again
  // at once without showing if it was open (the server closes them all on
  // a resync), else after the backoff.
  private fail() {
    const wasOpen = this.opened;
    this.close();
    const now = this.env.clock.now();
    const online = this.env.navigator.onLine;
    if (online && reconnectAtOnce(wasOpen, this.quickReconnects, now)) {
      this.connect(true);
      return;
    }
    const attempt = this.state.attempt + 1;
    const delay = getReconnectDelay(attempt);
    this.set({status: online ? "reconnecting" : "offline", attempt, nextAttemptAt: now + delay});
    this.retryTimer = this.env.clock.setTimeout(() => {
      this.retryTimer = undefined;
      this.set({nextAttemptAt: null});
      this.connect();
    }, delay);
    this.options.onError?.();
  }

  private close() {
    this.clearQuiet();
    this.source?.close();
    this.source = null;
  }

  private clearRetry() {
    if (this.retryTimer === undefined) return;
    this.env.clock.clearTimeout(this.retryTimer);
    this.retryTimer = undefined;
  }

  private clearQuiet() {
    if (this.quietTimer === undefined) return;
    this.env.clock.clearTimeout(this.quietTimer);
    this.quietTimer = undefined;
  }

  private stale(): boolean {
    const {staleAfter} = this.options;
    return staleAfter !== undefined && this.env.clock.now() - this.lastEventAt > staleAfter;
  }

  private readonly checkStale = () => {
    if (this.source && this.stale()) {
      console.info("The event stream went quiet; reconnecting");
      this.fail();
    }
  };

  private readonly handleOffline = () => {
    this.clearRetry();
    this.close();
    this.set({status: "offline", nextAttemptAt: null});
  };

  private readonly handleOnline = () => {
    this.reconnectNow();
  };

  // Browsers throttle timers in a background tab, so a reconnect may still
  // wait and the watchdog may not have looked: a tab that comes back opens
  // a stream that is closed or quiet at once.
  private readonly handleVisible = () => {
    if (this.env.document.visibilityState !== "visible") return;
    if (!this.source || this.source.readyState === CLOSED || this.stale()) this.reconnectNow();
  };

  private set(changes: Partial<ConnectionState>) {
    const next = {...this.state, ...changes};
    if (next.status === this.state.status && next.attempt === this.state.attempt && next.nextAttemptAt === this.state.nextAttemptAt) return;
    this.state = next;
    this.options.onChange?.(next);
  }
}
