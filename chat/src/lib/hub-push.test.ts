import {describe, expect, test} from "bun:test";
import {HubAPIError} from "./hub-api";
import {applicationServerKey, createHubPush, parseOpenMessage, PushPermissionError, pushScope, pushSupport, sameKey, type PushBrowser, type PushEnvironment} from "./hub-push";

const supported: PushEnvironment = {isSecureContext: true, hasServiceWorker: true, hasPushManager: true, hasNotification: true, ios: false, standalone: false};

describe("pushSupport", () => {
  test("needs a secure page", () => {
    expect(pushSupport({...supported, isSecureContext: false})).toBe("insecure");
  });
  test("is there with a service worker, push and notifications", () => {
    expect(pushSupport(supported)).toBe("supported");
  });
  test("on an iPhone, needs the page on the home screen", () => {
    const safari = {...supported, hasPushManager: false, hasNotification: false, ios: true};
    expect(pushSupport(safari)).toBe("home-screen");
    expect(pushSupport({...safari, standalone: true})).toBe("unsupported");
    expect(pushSupport({...supported, ios: true, standalone: true})).toBe("supported");
  });
  test("isn't in a browser without push", () => {
    expect(pushSupport({...supported, hasPushManager: false})).toBe("unsupported");
  });
});

// The hub's key of lib/webpush's tests (RFC 8291's sender).
const hubKey = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8";

test("applicationServerKey decodes base64url", () => {
  const key = applicationServerKey(hubKey);
  expect(key.length).toBe(65);
  expect(key[0]).toBe(4);
  expect(sameKey(key.buffer, hubKey)).toBe(true);
  const other = key.slice();
  other[64] ^= 1;
  expect(sameKey(other.buffer, hubKey)).toBe(false);
  expect(sameKey(null, hubKey)).toBe(false);
});

test("pushScope is the hub's page", () => {
  expect(pushScope("/chat")).toBe("/chat/hub/");
  expect(pushScope("")).toBe("/hub/");
});

test("parseOpenMessage takes only the worker's open message", () => {
  expect(parseOpenMessage({type: "agentapi-hub-open", id: "dev:1"})).toBe("dev:1");
  expect(parseOpenMessage({type: "agentapi-hub-open", id: ""})).toBeUndefined();
  expect(parseOpenMessage({type: "other", id: "dev:1"})).toBeUndefined();
  expect(parseOpenMessage("agentapi-hub-open")).toBeUndefined();
  expect(parseOpenMessage(null)).toBeUndefined();
});

// fakeSubscription stands for a browser's PushSubscription.
function fakeSubscription(endpoint: string, key: Uint8Array) {
  const subscription = {
    endpoint,
    unsubscribed: false,
    options: {applicationServerKey: key.buffer as ArrayBuffer},
    toJSON: () => ({endpoint, keys: {p256dh: "p256", auth: "auth"}}),
    unsubscribe: async () => {
      subscription.unsubscribed = true;
      return true;
    },
  };
  return subscription;
}

function fakeBrowser(permission: NotificationPermission, start?: ReturnType<typeof fakeSubscription>) {
  let current = start ?? null;
  const subscribed: ReturnType<typeof fakeSubscription>[] = [];
  const manager = {
    getSubscription: async () => (current && !current.unsubscribed ? current : null),
    subscribe: async (options: {applicationServerKey: Uint8Array}) => {
      current = fakeSubscription(`https://push.example/${subscribed.length + 1}`, options.applicationServerKey);
      subscribed.push(current);
      return current;
    },
  } as unknown as PushManager;
  const browser: PushBrowser = {register: async () => manager, existing: async () => manager, requestPermission: async () => permission};
  return {browser, subscribed, current: () => current};
}

function fakeHub(status = 200) {
  const calls: {method: string; path: string; body: unknown}[] = [];
  const fetcher = async (url: string, init?: RequestInit) => {
    const path = url.replace("https://hub", "");
    calls.push({method: init?.method ?? "GET", path, body: init?.body ? JSON.parse(String(init.body)) : undefined});
    if (path === "/api/push") return new Response(JSON.stringify({public_key: hubKey}), {status: 200});
    return new Response(status >= 300 ? JSON.stringify({code: "gone", message: "ended", error: "ended"}) : null, {status: status >= 300 ? status : 204});
  };
  return {calls, fetcher};
}

describe("createHubPush", () => {
  test("enable subscribes with the hub's key and tells the hub", async () => {
    const {browser, subscribed} = fakeBrowser("granted");
    const hub = fakeHub();
    await createHubPush("https://hub", browser, hub.fetcher).enable(true);
    expect(subscribed).toHaveLength(1);
    expect(sameKey(subscribed[0].options.applicationServerKey, hubKey)).toBe(true);
    expect(hub.calls.at(-1)).toEqual({method: "PUT", path: "/api/push/subscription", body: {endpoint: "https://push.example/1", keys: {p256dh: "p256", auth: "auth"}, finished: true}});
  });

  test("enable throws without permission", async () => {
    const {browser} = fakeBrowser("denied");
    const hub = fakeHub();
    expect(createHubPush("https://hub", browser, hub.fetcher).enable(false)).rejects.toBeInstanceOf(PushPermissionError);
    expect(hub.calls).toHaveLength(0);
  });

  test("a subscription of another key is made again", async () => {
    const old = fakeSubscription("https://push.example/old", new Uint8Array(65));
    const {browser, subscribed} = fakeBrowser("granted", old);
    const hub = fakeHub();
    await createHubPush("https://hub", browser, hub.fetcher).enable(false);
    expect(old.unsubscribed).toBe(true);
    expect(subscribed).toHaveLength(1);
    expect((hub.calls.at(-1)?.body as {endpoint: string}).endpoint).toBe("https://push.example/1");
  });

  test("the hub's key's subscription is kept", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser, subscribed} = fakeBrowser("granted", kept);
    const hub = fakeHub();
    await createHubPush("https://hub", browser, hub.fetcher).enable(false);
    expect(subscribed).toHaveLength(0);
    expect((hub.calls.at(-1)?.body as {endpoint: string}).endpoint).toBe("https://push.example/kept");
  });

  test("disable tells the hub and unsubscribes", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser} = fakeBrowser("granted", kept);
    const hub = fakeHub();
    await createHubPush("https://hub", browser, hub.fetcher).disable();
    expect(hub.calls).toEqual([{method: "DELETE", path: "/api/push/subscription", body: {endpoint: "https://push.example/kept"}}]);
    expect(kept.unsubscribed).toBe(true);
  });

  test("test names this browser's endpoint, and throws the hub's refusal", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser} = fakeBrowser("granted", kept);
    const ok = fakeHub();
    await createHubPush("https://hub", browser, ok.fetcher).test();
    expect(ok.calls).toEqual([{method: "POST", path: "/api/push/test", body: {endpoint: "https://push.example/kept"}}]);

    const gone = fakeHub(410);
    const error = await createHubPush("https://hub", browser, gone.fetcher)
      .test()
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(HubAPIError);
    expect((error as HubAPIError).status).toBe(410);
  });
});
