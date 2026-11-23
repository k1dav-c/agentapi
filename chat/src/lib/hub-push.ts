// Web Push for the hub's page: the hub pushes what needs you (and, if
// asked, the finished tasks) to the browsers that subscribed, through
// their push services, and the page's service worker (public/hub/sw.js)
// shows it with the page closed. A phone gets them once the hub's page is
// on its home screen (iOS) or in the browser (Android).

import {hubCall} from "./hub-api";
import type {HubWire} from "./hub";
import type {Fetcher} from "./hub-update";

// pushScope is where the hub's service worker works: the hub's page, below
// the chat's base path.
export function pushScope(basePath: string): string {
  return `${basePath}/hub/`;
}

// PushSupport says whether this browser can get the hub's pushes, and if
// not, why: a page that isn't secure (http), or a browser without push,
// which on an iPhone or iPad means the page isn't on the home screen.
export type PushSupport = "supported" | "insecure" | "home-screen" | "unsupported";

export interface PushEnvironment {
  isSecureContext: boolean;
  hasServiceWorker: boolean;
  hasPushManager: boolean;
  hasNotification: boolean;
  // An iPhone or iPad, which has push only for a page on the home screen.
  ios: boolean;
  standalone: boolean;
}

export function pushSupport(env: PushEnvironment): PushSupport {
  if (!env.isSecureContext) return "insecure";
  if (env.hasServiceWorker && env.hasPushManager && env.hasNotification) return "supported";
  if (env.ios && !env.standalone) return "home-screen";
  return "unsupported";
}

// browserPushEnvironment is this browser's PushEnvironment.
export function browserPushEnvironment(): PushEnvironment {
  if (typeof window === "undefined") {
    return {isSecureContext: false, hasServiceWorker: false, hasPushManager: false, hasNotification: false, ios: false, standalone: false};
  }
  const ua = navigator.userAgent;
  // iPadOS asks for desktop pages as a Mac with a touch screen.
  const ios = /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
  const standalone = window.matchMedia?.("(display-mode: standalone)").matches || (navigator as Navigator & {standalone?: boolean}).standalone === true;
  return {
    isSecureContext: window.isSecureContext,
    hasServiceWorker: "serviceWorker" in navigator,
    hasPushManager: "PushManager" in window,
    hasNotification: typeof Notification !== "undefined",
    ios,
    standalone,
  };
}

// applicationServerKey is the hub's VAPID key (base64url) as a browser
// subscribes with it.
export function applicationServerKey(publicKey: string): Uint8Array<ArrayBuffer> {
  const base64 = publicKey.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(base64 + "=".repeat((4 - (base64.length % 4)) % 4));
  const bytes = new Uint8Array(new ArrayBuffer(binary.length));
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

// sameKey says whether a subscription was made with the key: a hub that
// made a new one (its push.json went) can't push to the old subscription.
export function sameKey(subscriptionKey: ArrayBuffer | null | undefined, publicKey: string): boolean {
  if (!subscriptionKey) return false;
  const want = applicationServerKey(publicKey);
  const have = new Uint8Array(subscriptionKey);
  return have.length === want.length && have.every((byte, i) => byte === want[i]);
}

// The browser's side of a subscription, for tests to stand in for.
export interface PushBrowser {
  register(): Promise<PushManager>;
  existing(): Promise<PushManager | undefined>;
  requestPermission(): Promise<NotificationPermission>;
}

export function browserPush(basePath: string): PushBrowser {
  const scope = pushScope(basePath);
  return {
    register: async () => {
      await navigator.serviceWorker.register(`${scope}sw.js`, {scope});
      return (await navigator.serviceWorker.ready).pushManager;
    },
    existing: async () => (await navigator.serviceWorker.getRegistration(scope))?.pushManager,
    requestPermission: () => (Notification.permission === "default" ? Notification.requestPermission() : Promise.resolve(Notification.permission)),
  };
}

export class PushPermissionError extends Error {
  constructor() {
    super("Notifications are blocked for this site");
    this.name = "PushPermissionError";
  }
}

// createHubPush is the page's push: subscribe this browser to the hub,
// keep the hub's copy current, stop, and test.
export function createHubPush(root: string, browser: PushBrowser, fetcher: Fetcher = (url, init) => fetch(url, init)) {
  const call = hubCall(root, fetcher);
  const json = (method: string, body: unknown): RequestInit => ({method, headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  const publicKey = async (): Promise<string> => {
    const info = (await (await call("/api/push")).json()) as Partial<HubWire["PushInfo"]>;
    if (typeof info.public_key !== "string" || !info.public_key) throw new Error("The hub's answer has no push key.");
    return info.public_key;
  };
  // subscribe subscribes with the hub's key (again, if the browser's
  // subscription is another key's) and tells the hub.
  const subscribe = async (manager: PushManager, finished: boolean): Promise<void> => {
    const key = await publicKey();
    let subscription = await manager.getSubscription();
    if (subscription && !sameKey(subscription.options.applicationServerKey, key)) {
      await subscription.unsubscribe();
      subscription = null;
    }
    subscription ??= await manager.subscribe({userVisibleOnly: true, applicationServerKey: applicationServerKey(key)});
    const {endpoint, keys} = subscription.toJSON();
    await call("/api/push/subscription", json("PUT", {endpoint: endpoint ?? "", keys: {p256dh: keys?.p256dh ?? "", auth: keys?.auth ?? ""}, finished} satisfies HubWire["PushSubscribeBody"]));
  };
  const endpoint = async (): Promise<string | undefined> => (await (await browser.existing())?.getSubscription())?.endpoint;
  return {
    // enable asks for permission and subscribes; it throws
    // PushPermissionError when the browser won't notify.
    enable: async (finished: boolean): Promise<void> => {
      if ((await browser.requestPermission()) !== "granted") throw new PushPermissionError();
      await subscribe(await browser.register(), finished);
    },
    // sync tells the hub this browser's subscription again (the hub
    // forgets one its push service ended, or a new key replaced), with
    // what to push; one the browser dropped is made again.
    sync: async (finished: boolean): Promise<void> => {
      if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
      await subscribe(await browser.register(), finished);
    },
    // disable unsubscribes, and tells the hub.
    disable: async (): Promise<void> => {
      const subscription = await (await browser.existing())?.getSubscription();
      if (!subscription) return;
      try {
        await call("/api/push/subscription", json("DELETE", {endpoint: subscription.endpoint} satisfies HubWire["PushEndpointBody"]));
      } finally {
        await subscription.unsubscribe();
      }
    },
    // test has the hub push a test notification to this browser.
    test: async (): Promise<void> => {
      const at = await endpoint();
      if (!at) throw new Error("This browser isn't subscribed.");
      await call("/api/push/test", json("POST", {endpoint: at} satisfies HubWire["PushEndpointBody"]));
    },
  };
}

export type HubPush = ReturnType<typeof createHubPush>;

// openMessage is the service worker's message to the page that a click
// on a notification focused: open the agent.
export interface OpenMessage {
  type: "agentapi-hub-open";
  id: string;
}

export function parseOpenMessage(data: unknown): string | undefined {
  if (typeof data !== "object" || data === null) return undefined;
  const message = data as Partial<OpenMessage>;
  return message.type === "agentapi-hub-open" && typeof message.id === "string" && message.id ? message.id : undefined;
}
