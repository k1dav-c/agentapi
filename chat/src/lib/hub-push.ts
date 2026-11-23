// Web Push for the hub's page, the hub's side: subscribing this browser
// to the hub with its key, keeping the hub's copy current, and the devices
// the hub pushes to with their settings (lib/hub/push_api.go). What the
// browser can do, and the service worker's side, are in
// hub-push-browser.ts.

import {hubCall} from "./hub-api";
import type {HubWire} from "./hub";
import {PushPermissionError, type PushBrowser} from "./hub-push-browser";
import type {Fetcher} from "./hub-update";

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

// PushDevice is a device the hub pushes to, as the Devices settings show
// it; PushPrefsChange, what they change of one.
export type PushDevice = HubWire["PushDevice"];
export type PushPrefsChange = HubWire["PushPrefsChange"];
export type QuietHours = HubWire["QuietHours"];

// deviceID is the hub's ID of the device with the endpoint (pushDeviceID
// in lib/hub/push.go): the first 8 bytes of its SHA-256, in hex.
export async function deviceID(endpoint: string): Promise<string> {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(endpoint)));
  return [...digest.slice(0, 8)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

// createHubPush is the page's push: subscribe this browser to the hub,
// keep the hub's copy current, stop and test, and the devices the hub
// pushes to with their settings.
export function createHubPush(root: string, browser: PushBrowser, label: string, fetcher: Fetcher = (url, init) => fetch(url, init)) {
  const call = hubCall(root, fetcher);
  const json = (method: string, body: unknown): RequestInit => ({method, headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  const publicKey = async (): Promise<string> => {
    const info = (await (await call("/api/push")).json()) as Partial<HubWire["PushInfo"]>;
    if (typeof info.public_key !== "string" || !info.public_key) throw new Error("The hub's answer has no push key.");
    return info.public_key;
  };
  // subscribe subscribes with the hub's key (again, if the browser's
  // subscription is another key's) and tells the hub, which keeps the
  // device's settings.
  const subscribe = async (manager: PushManager): Promise<void> => {
    const key = await publicKey();
    let subscription = await manager.getSubscription();
    if (subscription && !sameKey(subscription.options.applicationServerKey, key)) {
      await subscription.unsubscribe();
      subscription = null;
    }
    subscription ??= await manager.subscribe({userVisibleOnly: true, applicationServerKey: applicationServerKey(key)});
    const {endpoint, keys} = subscription.toJSON();
    await call("/api/push/subscription", json("PUT", {endpoint: endpoint ?? "", keys: {p256dh: keys?.p256dh ?? "", auth: keys?.auth ?? ""}, label} satisfies HubWire["PushSubscribeBody"]));
  };
  const endpoint = async (): Promise<string | undefined> => (await (await browser.existing())?.getSubscription())?.endpoint;
  const device = (id: string) => `/api/push/devices/${encodeURIComponent(id)}`;
  return {
    // enable asks for permission and subscribes; it throws
    // PushPermissionError when the browser won't notify.
    enable: async (): Promise<void> => {
      if ((await browser.requestPermission()) !== "granted") throw new PushPermissionError();
      await subscribe(await browser.register());
    },
    // sync tells the hub this browser's subscription again: the hub keeps
    // a device while the session of the page that last told it holds, and
    // forgets one its push service ended or a new key replaced. One the
    // browser dropped is made again.
    sync: async (): Promise<void> => {
      if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
      await subscribe(await browser.register());
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
    // thisDevice is this browser's device ID, when it is subscribed.
    thisDevice: async (): Promise<string | undefined> => {
      const at = await endpoint();
      return at ? deviceID(at) : undefined;
    },
    // devices lists the devices the hub pushes to.
    devices: async (): Promise<PushDevice[]> => {
      const body = (await (await call("/api/push/devices")).json()) as Partial<HubWire["PushDevicesBody"]>;
      if (!Array.isArray(body.devices)) throw new Error("The hub's answer has no devices.");
      return body.devices;
    },
    // change changes a device's settings, and returns the device.
    change: async (id: string, change: PushPrefsChange): Promise<PushDevice> => (await (await call(device(id), json("PATCH", change))).json()) as PushDevice,
    // remove stops pushing to a device.
    remove: async (id: string): Promise<void> => {
      await call(device(id), {method: "DELETE"});
    },
  };
}

export type HubPush = ReturnType<typeof createHubPush>;
