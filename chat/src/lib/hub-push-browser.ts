// Web Push for the hub's page, the browser's side: whether this browser
// can get the hub's pushes, the service worker that shows them
// (public/hub/sw.js) and the subscription it holds, the device's name, the
// worker's messages to the page and the app icon's badge. A phone gets
// pushes once the hub's page is on its home screen (iOS) or in the browser
// (Android). The hub's side is hub-push.ts.

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

// deviceLabel names this browser's device for the hub's list of them:
// the platform, then the browser or, for an app on the home screen, that.
export function deviceLabel(userAgent: string, standalone: boolean, touch = 0): string {
  const platform = /iPhone|iPod/.test(userAgent)
    ? "iPhone"
    : /iPad/.test(userAgent) || (/Macintosh/.test(userAgent) && touch > 1)
      ? "iPad"
      : /Android/.test(userAgent)
        ? "Android"
        : /Macintosh|Mac OS X/.test(userAgent)
          ? "Mac"
          : /Windows/.test(userAgent)
            ? "Windows"
            : /CrOS/.test(userAgent)
              ? "ChromeOS"
              : /Linux/.test(userAgent)
                ? "Linux"
                : "";
  const browser = standalone
    ? "Home Screen"
    : /Edg\//.test(userAgent)
      ? "Edge"
      : /Firefox\/|FxiOS/.test(userAgent)
        ? "Firefox"
        : /Chrome\/|CriOS|Chromium/.test(userAgent)
          ? "Chrome"
          : /Safari\//.test(userAgent)
            ? "Safari"
            : "Browser";
  return platform ? `${platform} · ${browser}` : browser;
}

// The service worker's messages to the page that a click on a
// notification focused: open the agent, or the issue in Issues.
export type OpenTarget = {agent: string} | {issue: string};

export function parseOpenMessage(data: unknown): OpenTarget | undefined {
  if (typeof data !== "object" || data === null) return undefined;
  const message = data as {type?: unknown; id?: unknown; key?: unknown};
  if (message.type === "agentapi-hub-open" && typeof message.id === "string" && message.id) return {agent: message.id};
  if (message.type === "agentapi-hub-issue" && typeof message.key === "string" && message.key) return {issue: message.key};
  return undefined;
}

// setAppBadge shows count on the app's icon (an app on the home screen,
// or an installed one), or clears it at 0, where the browser can.
export function setAppBadge(count: number, nav: Partial<Pick<Navigator, "setAppBadge" | "clearAppBadge">> = typeof navigator === "undefined" ? {} : navigator) {
  const done = count > 0 ? nav.setAppBadge?.(count) : nav.clearAppBadge?.();
  done?.catch(() => {
    // No permission, or no badge here.
  });
}
