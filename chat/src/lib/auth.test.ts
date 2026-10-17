import {afterEach, describe, expect, test} from "bun:test";
import {
  authFetch,
  bootstrapSession,
  ensureSession,
  exchangeToken,
  isAuthRequired,
  isCrossOrigin,
  markAuthRequired,
  requestCredentials,
  requestSignIn,
  resetAuthState,
  serverHost,
  setSignInHandler,
  takeTokenFromHash,
  withAuthRetry,
  withCredentials,
} from "./auth";

const realFetch = globalThis.fetch;
const BASE = "http://agentapi.test";

interface Call {
  url: string;
  init: RequestInit;
}

// Replaces fetch with one that answers from responses in order and records
// each call.
function mockFetch(...responses: Response[]): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
    calls.push({url: String(url), init});
    const next = responses.shift();
    if (!next) throw new Error(`unexpected fetch ${url}`);
    return next;
  }) as typeof fetch;
  return calls;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {status, headers: {"Content-Type": "application/json"}});
const unauthorized = () => new Response("Unauthorized", {status: 401});

// Counts the sign-in prompts; each answers with the next result.
function dialog(...results: boolean[]) {
  const prompts: string[] = [];
  setSignInHandler(async (baseURL) => {
    prompts.push(baseURL);
    return results.shift() ?? false;
  });
  return prompts;
}

// The page the chat runs on: bun has no location or history.
function page({origin = "http://agentapi.test", pathname = "/chat/", search = "", hash = ""} = {}) {
  const replaced: string[] = [];
  Object.assign(globalThis, {
    location: {origin, pathname, search, hash},
    history: {state: {kept: true}, replaceState: (_state: unknown, _title: string, url: string) => replaced.push(url)},
  });
  return replaced;
}

afterEach(() => {
  globalThis.fetch = realFetch;
  Reflect.deleteProperty(globalThis, "location");
  Reflect.deleteProperty(globalThis, "history");
  resetAuthState();
});

describe("takeTokenFromHash", () => {
  test("takes the token and keeps the rest of the fragment", () => {
    expect(takeTokenFromHash("#token=abc123")).toEqual({token: "abc123", hash: ""});
    expect(takeTokenFromHash("#token=abc&x=1")).toEqual({token: "abc", hash: "#x=1"});
    expect(takeTokenFromHash("#x=1&token=a%26b")).toEqual({token: "a&b", hash: "#x=1"});
  });
  test("no token", () => {
    expect(takeTokenFromHash("")).toEqual({token: null, hash: ""});
    expect(takeTokenFromHash("#section")).toEqual({token: null, hash: "#section"});
    expect(takeTokenFromHash("#token=")).toEqual({token: null, hash: ""});
  });
});

describe("isCrossOrigin and serverHost", () => {
  test("compares origins", () => {
    expect(isCrossOrigin("http://localhost:3284", "http://localhost:3284")).toBe(false);
    expect(isCrossOrigin("http://localhost:3284/proxy/app", "http://localhost:3284")).toBe(false);
    expect(isCrossOrigin("/relative", "http://localhost:3284")).toBe(false);
    expect(isCrossOrigin("http://localhost:3284", "http://localhost:3000")).toBe(true);
  });

  test("names the server", () => {
    expect(serverHost("http://localhost:3284/proxy/app", "http://localhost:3000")).toBe("localhost:3284");
    expect(serverHost("/proxy/app", "https://example.com")).toBe("example.com");
    expect(serverHost("not a url")).toBe("not a url");
  });
});

describe("exchangeToken", () => {
  test("posts the token as a Bearer header and checks the cookie stuck", async () => {
    const calls = mockFetch(json({auth_required: true, authenticated: true}), json({auth_required: true, authenticated: true}));
    expect(await exchangeToken(BASE, "s3cret")).toBe("signed-in");
    expect(calls[0].url).toBe(`${BASE}/auth/session`);
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.credentials).toBe("include");
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBe("Bearer s3cret");
    expect(calls[1].init.method).toBeUndefined();
    expect(calls[1].init.credentials).toBe("include");
    expect(isAuthRequired(BASE)).toBe(true);
  });

  test("a rejected token", async () => {
    mockFetch(unauthorized());
    expect(await exchangeToken(BASE, "nope")).toBe("rejected");
  });

  test("the browser didn't keep the cookie", async () => {
    mockFetch(json({auth_required: true, authenticated: true}), json({auth_required: true, authenticated: false}));
    expect(await exchangeToken(BASE, "s3cret")).toBe("cookie-blocked");
  });

  test("a server without --api-token needs no credentials", async () => {
    const calls = mockFetch(json({auth_required: false, authenticated: true}));
    expect(await exchangeToken(BASE, "anything")).toBe("not-required");
    expect(calls).toHaveLength(1);
    expect(isAuthRequired(BASE)).toBe(false);
    expect(requestCredentials(BASE)).toBe("same-origin");
  });

  test("even after an earlier 401 marked it", async () => {
    markAuthRequired(BASE);
    mockFetch(json({auth_required: false, authenticated: true}));
    expect(await exchangeToken(BASE, "anything")).toBe("not-required");
    expect(isAuthRequired(BASE)).toBe(false);
  });
});

describe("authFetch", () => {
  test("passes through when the server needs no token", async () => {
    const calls = mockFetch(json({ok: true}));
    const response = await authFetch(BASE, `${BASE}/status`);
    expect(response.status).toBe(200);
    expect(calls).toHaveLength(1);
    expect(calls[0].init.credentials).toBe("same-origin");
    expect(requestCredentials(BASE)).toBe("same-origin");
  });

  test("asks for the token on a 401 and retries with credentials", async () => {
    const calls = mockFetch(unauthorized(), json({ok: true}));
    const prompts = dialog(true);
    const response = await authFetch(BASE, `${BASE}/message`, {method: "POST", body: "{}"});
    expect(response.status).toBe(200);
    expect(prompts).toEqual([BASE]);
    expect(calls).toHaveLength(2);
    expect(calls[1].init.method).toBe("POST");
    expect(calls[1].init.body).toBe("{}");
    expect(calls[1].init.credentials).toBe("include");
    expect(requestCredentials(BASE)).toBe("include");
  });

  test("returns the 401 when the dialog is cancelled", async () => {
    mockFetch(unauthorized());
    dialog(false);
    const response = await authFetch(BASE, `${BASE}/status`);
    expect(response.status).toBe(401);
  });

  test("across origins it first retries with the cookie it didn't send", async () => {
    page({origin: "http://localhost:3000"});
    const calls = mockFetch(unauthorized(), json({ok: true}));
    const prompts = dialog(true);
    const response = await authFetch(BASE, `${BASE}/status`);
    expect(response.status).toBe(200);
    expect(calls.map((call) => call.init.credentials)).toEqual(["same-origin", "include"]);
    expect(prompts).toEqual([]);
    expect(withCredentials(BASE)).toBe(true);
  });

  test("across origins it asks when the retry gets a 401 too", async () => {
    page({origin: "http://localhost:3000"});
    const calls = mockFetch(unauthorized(), unauthorized(), json({ok: true}));
    const prompts = dialog(true);
    const response = await authFetch(BASE, `${BASE}/status`);
    expect(response.status).toBe(200);
    expect(calls.map((call) => call.init.credentials)).toEqual(["same-origin", "include", "include"]);
    expect(prompts).toEqual([BASE]);
  });
});

describe("withAuthRetry", () => {
  test("retries any request, such as an upload, with the credentials to use", async () => {
    page({origin: "http://localhost:3000"});
    const prompts = dialog(true);
    const sent: RequestCredentials[] = [];
    const statuses = [401, 401, 200];
    const result = await withAuthRetry(BASE, async (credentials) => {
      sent.push(credentials);
      return {status: statuses.shift() ?? 500, credentials};
    });
    expect(result).toEqual({status: 200, credentials: "include"});
    expect(sent).toEqual(["same-origin", "include", "include"]);
    expect(prompts).toEqual([BASE]);
  });
});

describe("requestSignIn", () => {
  test("concurrent requests share one prompt", async () => {
    let prompts = 0;
    let finish: (ok: boolean) => void = () => {};
    setSignInHandler(() => {
      prompts++;
      return new Promise<boolean>((resolve) => { finish = resolve; });
    });
    const first = requestSignIn(BASE);
    const second = requestSignIn(BASE);
    finish(true);
    expect(await first).toBe(true);
    expect(await second).toBe(true);
    expect(prompts).toBe(1);
  });

  test("without a dialog nothing is asked", async () => {
    expect(await requestSignIn(BASE)).toBe(false);
  });

  test("background checks stop asking after a cancel", async () => {
    const prompts = dialog(false, false);
    expect(await requestSignIn(BASE)).toBe(false);
    expect(await requestSignIn(BASE, {background: true})).toBe(false);
    expect(prompts).toHaveLength(1);
    // Something the user did still asks.
    expect(await requestSignIn(BASE)).toBe(false);
    expect(prompts).toHaveLength(2);
  });
});

describe("ensureSession", () => {
  test("signed in or no auth: no prompt", async () => {
    const prompts = dialog(true);
    mockFetch(json({auth_required: false, authenticated: true}));
    expect(await ensureSession(BASE)).toBe(false);
    expect(isAuthRequired(BASE)).toBe(false);
    mockFetch(json({auth_required: true, authenticated: true}));
    expect(await ensureSession(BASE)).toBe(false);
    expect(prompts).toEqual([]);
  });

  test("asks when the server needs a token", async () => {
    dialog(true);
    mockFetch(json({auth_required: true, authenticated: false}));
    expect(await ensureSession(BASE)).toBe(true);
    expect(isAuthRequired(BASE)).toBe(true);
  });

  test("across origins it reads the session again with the cookie", async () => {
    page({origin: "http://localhost:3000"});
    const prompts = dialog(true);
    const calls = mockFetch(json({auth_required: true, authenticated: false}), json({auth_required: true, authenticated: true}));
    expect(await ensureSession(BASE)).toBe(false);
    expect(calls.map((call) => call.init.credentials)).toEqual(["same-origin", "include"]);
    expect(prompts).toEqual([]);
  });

  test("after a cancelled dialog only the user asks again", async () => {
    const prompts = dialog(false, true);
    const locked = () => json({auth_required: true, authenticated: false});
    mockFetch(locked(), locked(), locked());
    expect(await ensureSession(BASE)).toBe(false);
    expect(prompts).toHaveLength(1);
    // A reconnect in the background stays quiet...
    expect(await ensureSession(BASE, {background: true})).toBe(false);
    expect(prompts).toHaveLength(1);
    // ...until the user asks for it (Reconnect now).
    expect(await ensureSession(BASE)).toBe(true);
    expect(prompts).toHaveLength(2);
  });

  test("older servers without /auth/session", async () => {
    mockFetch(new Response("Not Found", {status: 404}));
    expect(await ensureSession(BASE)).toBe(false);
  });
});

describe("bootstrapSession", () => {
  test("signs in with a #token= link and removes the token from the address bar", async () => {
    const replaced = page({search: "?title=Docs", hash: "#token=s3cret&x=1"});
    const prompts = dialog(true);
    const calls = mockFetch(json({auth_required: true, authenticated: true}), json({auth_required: true, authenticated: true}));
    await bootstrapSession(BASE);
    expect(replaced).toEqual(["/chat/?title=Docs#x=1"]);
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBe("Bearer s3cret");
    expect(calls).toHaveLength(2);
    expect(prompts).toEqual([]);
    // Once per server.
    await bootstrapSession(BASE);
    expect(calls).toHaveLength(2);
  });

  test("a link with a wrong token falls back to the dialog", async () => {
    const replaced = page({hash: "#token=old"});
    const prompts = dialog(true);
    mockFetch(unauthorized(), json({auth_required: true, authenticated: false}));
    await bootstrapSession(BASE);
    expect(replaced).toEqual(["/chat/"]);
    expect(prompts).toEqual([BASE]);
  });

  test("without a token it checks the session", async () => {
    const replaced = page();
    const prompts = dialog(true);
    mockFetch(json({auth_required: true, authenticated: false}));
    await bootstrapSession(BASE);
    expect(replaced).toEqual([]);
    expect(prompts).toEqual([BASE]);
  });
});
