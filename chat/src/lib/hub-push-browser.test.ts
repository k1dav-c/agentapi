import {describe, expect, test} from "bun:test";
import {deviceLabel, parseOpenMessage, pushScope, pushSupport, setAppBadge, type PushEnvironment} from "./hub-push-browser";

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

test("pushScope is the hub's page", () => {
  expect(pushScope("/chat")).toBe("/chat/hub/");
  expect(pushScope("")).toBe("/hub/");
});

test("parseOpenMessage takes only the worker's open messages", () => {
  expect(parseOpenMessage({type: "agentapi-hub-open", id: "dev:1"})).toEqual({agent: "dev:1"});
  expect(parseOpenMessage({type: "agentapi-hub-issue", key: "acme~widget~12"})).toEqual({issue: "acme~widget~12"});
  expect(parseOpenMessage({type: "agentapi-hub-issue", key: 12})).toBeUndefined();
  expect(parseOpenMessage({type: "agentapi-hub-open", id: ""})).toBeUndefined();
  expect(parseOpenMessage({type: "other", id: "dev:1"})).toBeUndefined();
  expect(parseOpenMessage("agentapi-hub-open")).toBeUndefined();
  expect(parseOpenMessage(null)).toBeUndefined();
});

describe("deviceLabel", () => {
  const iphone = "Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148";
  test("names the platform and the browser", () => {
    expect(deviceLabel(iphone, true)).toBe("iPhone · Home Screen");
    expect(deviceLabel("Mozilla/5.0 (Linux; Android 16; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Mobile Safari/537.36", false)).toBe("Android · Chrome");
    expect(deviceLabel("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Safari/605.1.15", false)).toBe("Mac · Safari");
    expect(deviceLabel("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Safari/605.1.15", true, 5)).toBe("iPad · Home Screen");
    expect(deviceLabel("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36 Edg/141.0", false)).toBe("Windows · Edge");
    expect(deviceLabel("Mozilla/5.0 (X11; Linux x86_64; rv:145.0) Gecko/20100101 Firefox/145.0", false)).toBe("Linux · Firefox");
    expect(deviceLabel("curl/8", false)).toBe("Browser");
  });
});

test("setAppBadge sets the count, and clears it at 0", () => {
  const calls: string[] = [];
  const nav = {setAppBadge: async (n?: number) => void calls.push(`set ${n}`), clearAppBadge: async () => void calls.push("clear")};
  setAppBadge(3, nav);
  setAppBadge(0, nav);
  setAppBadge(2, {});
  expect(calls).toEqual(["set 3", "clear"]);
});
