import {describe, expect, test} from "bun:test";
import {HubAPIError} from "./hub-api";
import {applicationServerKey, createHubPush, deviceID, sameKey} from "./hub-push";
import {PushPermissionError, type PushBrowser} from "./hub-push-browser";

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
    await createHubPush("https://hub", browser, "Pixel · Chrome", hub.fetcher).enable();
    expect(subscribed).toHaveLength(1);
    expect(sameKey(subscribed[0].options.applicationServerKey, hubKey)).toBe(true);
    expect(hub.calls.at(-1)).toEqual({method: "PUT", path: "/api/push/subscription", body: {endpoint: "https://push.example/1", keys: {p256dh: "p256", auth: "auth"}, label: "Pixel · Chrome", enable: true}});
  });

  test("enable throws without permission", async () => {
    const {browser} = fakeBrowser("denied");
    const hub = fakeHub();
    expect(createHubPush("https://hub", browser, "Pixel · Chrome", hub.fetcher).enable()).rejects.toBeInstanceOf(PushPermissionError);
    expect(hub.calls).toHaveLength(0);
  });

  test("a subscription of another key is made again", async () => {
    const old = fakeSubscription("https://push.example/old", new Uint8Array(65));
    const {browser, subscribed} = fakeBrowser("granted", old);
    const hub = fakeHub();
    await createHubPush("https://hub", browser, "Pixel · Chrome", hub.fetcher).enable();
    expect(old.unsubscribed).toBe(true);
    expect(subscribed).toHaveLength(1);
    expect((hub.calls.at(-1)?.body as {endpoint: string}).endpoint).toBe("https://push.example/1");
  });

  test("the hub's key's subscription is kept", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser, subscribed} = fakeBrowser("granted", kept);
    const hub = fakeHub();
    await createHubPush("https://hub", browser, "Pixel · Chrome", hub.fetcher).enable();
    expect(subscribed).toHaveLength(0);
    expect((hub.calls.at(-1)?.body as {endpoint: string}).endpoint).toBe("https://push.example/kept");
  });

  test("disable tells the hub and unsubscribes", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser} = fakeBrowser("granted", kept);
    const hub = fakeHub();
    await createHubPush("https://hub", browser, "Pixel · Chrome", hub.fetcher).disable();
    expect(hub.calls).toEqual([{method: "DELETE", path: "/api/push/subscription", body: {endpoint: "https://push.example/kept"}}]);
    expect(kept.unsubscribed).toBe(true);
  });

  test("test names this browser's endpoint, and throws the hub's refusal", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser} = fakeBrowser("granted", kept);
    const ok = fakeHub();
    await createHubPush("https://hub", browser, "Pixel · Chrome", ok.fetcher).test();
    expect(ok.calls).toEqual([{method: "POST", path: "/api/push/test", body: {endpoint: "https://push.example/kept"}}]);

    const gone = fakeHub(410);
    const error = await createHubPush("https://hub", browser, "Pixel · Chrome", gone.fetcher)
      .test()
      .catch((e: unknown) => e);
    expect(error).toBeInstanceOf(HubAPIError);
    expect((error as HubAPIError).status).toBe(410);
  });
});

test("deviceID is the hub's: the start of the endpoint's SHA-256", async () => {
  // sha256("https://push.example/1"), as lib/hub's pushDeviceID has it.
  expect(await deviceID("https://push.example/1")).toBe("5ba15fff965f44af");
  expect(await deviceID("https://push.example/1")).toBe(await deviceID("https://push.example/1"));
  expect(await deviceID("https://push.example/1")).not.toBe(await deviceID("https://push.example/2"));
});

describe("the devices", () => {
  test("list, change and remove by ID", async () => {
    const calls: {method: string; path: string; body?: unknown}[] = [];
    const fetcher = async (url: string, init?: RequestInit) => {
      const path = url.replace("https://hub", "");
      calls.push({method: init?.method ?? "GET", path, body: init?.body ? JSON.parse(String(init.body)) : undefined});
      if (path === "/api/push/devices") return new Response(JSON.stringify({devices: [{id: "ab", created_at: "2026-11-23T00:00:00Z", finished: false, issues: false, groups: [], pinned: false}]}));
      if (init?.method === "PATCH") return new Response(JSON.stringify({id: "ab", created_at: "2026-11-23T00:00:00Z", finished: true, issues: false, groups: [], pinned: false}));
      return new Response(null, {status: 204});
    };
    const push = createHubPush("https://hub", fakeBrowser("granted").browser, "Mac · Safari", fetcher);
    expect((await push.devices()).map((device) => device.id)).toEqual(["ab"]);
    expect((await push.change("ab", {finished: true})).finished).toBe(true);
    await push.remove("ab");
    expect(calls.slice(1)).toEqual([
      {method: "PATCH", path: "/api/push/devices/ab", body: {finished: true}},
      {method: "DELETE", path: "/api/push/devices/ab", body: undefined},
    ]);
  });
});

describe("renewing", () => {
  test("sync renews without enable, and throws the hub's 410 for a removed device", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser} = fakeBrowser("granted", kept);
    const gone = fakeHub(410);
    globalThis.Notification ??= {permission: "granted"} as unknown as typeof Notification;
    const error = await createHubPush("https://hub", browser, "Mac · Safari", gone.fetcher)
      .sync()
      .catch((e: unknown) => e);
    expect(gone.calls.at(-1)?.body).toEqual({endpoint: "https://push.example/kept", keys: {p256dh: "p256", auth: "auth"}, label: "Mac · Safari", enable: false});
    expect((error as HubAPIError).status).toBe(410);
  });

  test("forget unsubscribes without telling the hub", async () => {
    const kept = fakeSubscription("https://push.example/kept", applicationServerKey(hubKey));
    const {browser} = fakeBrowser("granted", kept);
    const hub = fakeHub();
    const push = createHubPush("https://hub", browser, "Mac · Safari", hub.fetcher);
    expect(await push.endpoint()).toBe("https://push.example/kept");
    await push.forget();
    expect(kept.unsubscribed).toBe(true);
    expect(hub.calls).toHaveLength(0);
  });
});
