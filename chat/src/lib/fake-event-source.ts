// Fakes of what an EventSourceConnection uses of the browser, for tests:
// event sources driven by hand, a clock that moves when told, and the
// window, document and navigator events.

import type {Clock, ConnectionEnvironment, EventSourceLike} from "./event-source";

export class FakeEventSource implements EventSourceLike {
  readyState = 0;
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  private listeners = new Map<string, ((event: MessageEvent<string>) => void)[]>();

  constructor(
    readonly url: string,
    readonly init?: EventSourceInit,
  ) {}

  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  close() {
    this.readyState = 2;
  }

  get closed(): boolean {
    return this.readyState === 2;
  }

  open() {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }

  // Sends an event with data as JSON, and an SSE id when given.
  emit(type: string, data: unknown = {}, lastEventId = "") {
    if (this.closed) return;
    const event = new MessageEvent<string>(type, {data: JSON.stringify(data), lastEventId});
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }

  // The stream ended or couldn't open (a response that isn't a 200).
  fail() {
    this.onerror?.(new Event("error"));
  }
}

export class FakeClock implements Clock {
  time = 1_000_000;
  private timers = new Map<number, {at: number; every?: number; callback: () => void}>();
  private nextId = 1;

  now() {
    return this.time;
  }

  setTimeout(callback: () => void, ms: number) {
    const id = this.nextId++;
    this.timers.set(id, {at: this.time + ms, callback});
    return id;
  }

  clearTimeout(timer: unknown) {
    this.timers.delete(timer as number);
  }

  setInterval(callback: () => void, ms: number) {
    const id = this.nextId++;
    this.timers.set(id, {at: this.time + ms, every: ms, callback});
    return id;
  }

  clearInterval(timer: unknown) {
    this.timers.delete(timer as number);
  }

  // Moves the time on by ms, running the timers due on the way in order.
  advance(ms: number) {
    const end = this.time + ms;
    for (;;) {
      let next: [number, {at: number; every?: number; callback: () => void}] | undefined;
      for (const entry of this.timers) {
        if (entry[1].at <= end && (!next || entry[1].at < next[1].at)) next = entry;
      }
      if (!next) break;
      const [id, timer] = next;
      this.time = timer.at;
      if (timer.every) timer.at += timer.every;
      else this.timers.delete(id);
      timer.callback();
    }
    this.time = end;
  }
}

export interface FakeEnvironment extends ConnectionEnvironment {
  clock: FakeClock;
  navigator: {onLine: boolean};
  sources: FakeEventSource[];
  // The latest event source.
  latest(): FakeEventSource;
  setOnline(online: boolean): void;
  setVisible(visible: boolean): void;
}

export function fakeEnvironment(): FakeEnvironment {
  const sources: FakeEventSource[] = [];
  const window = new EventTarget();
  const document = Object.assign(new EventTarget(), {visibilityState: "visible"});
  const navigator = {onLine: true};
  return {
    EventSource: class extends FakeEventSource {
      constructor(url: string, init?: EventSourceInit) {
        super(url, init);
        sources.push(this);
      }
    },
    clock: new FakeClock(),
    window,
    document,
    navigator,
    sources,
    latest: () => sources[sources.length - 1],
    setOnline(online) {
      navigator.onLine = online;
      window.dispatchEvent(new Event(online ? "online" : "offline"));
    },
    setVisible(visible) {
      document.visibilityState = visible ? "visible" : "hidden";
      document.dispatchEvent(new Event("visibilitychange"));
    },
  };
}
