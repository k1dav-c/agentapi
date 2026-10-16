import {afterEach, describe, expect, test} from "bun:test";
import {
  authFetch,
  ensureSession,
  exchangeToken,
  isAuthRequired,
  isCrossOrigin,
  requestCredentials,
  requestSignIn,
  setSignInHandler,
  takeTokenFromHash,
} from "./auth";

const realFetch = globalThis.fetch;

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

afterEach(() => {
  globalThis.fetch = realFetch;
  setSignInHandler(null);
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

describe("isCrossOrigin", () => {
  test("compares origins", () => {
    expect(isCrossOrigin("http://localhost:3284", "http://localhost:3284")).toBe(false);
    expect(isCrossOrigin("http://localhost:3284/proxy/app", "http://localhost:3284")).toBe(false);
    expect(isCrossOrigin("/relative", "http://localhost:3284")).toBe(false);
    expect(isCrossOrigin("http://localhost:3284", "http://localhost:3000")).toBe(true);
  });
});

describe("exchangeToken", () => {
  test("posts the token as a Bearer header with credentials", async () => {
    const calls = mockFetch(json({auth_required: true, authenticated: true}));
    expect(await exchangeToken("http://exchange.test", "s3cret")).toBe(true);
    expect(calls[0].url).toBe("http://exchange.test/auth/session");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.credentials).toBe("include");
    expect((calls[0].init.headers as Record<string, string>).Authorization).toBe("Bearer s3cret");
    expect(isAuthRequired("http://exchange.test")).toBe(true);
  });
  test("a rejected token", async () => {
    mockFetch(new Response("Unauthorized", {status: 401}));
    expect(await exchangeToken("http://rejected.test", "nope")).toBe(false);
  });
});

describe("authFetch", () => {
  test("passes through when the server needs no token", async () => {
    const calls = mockFetch(json({ok: true}));
    const response = await authFetch("http://open.test", "http://open.test/status");
    expect(response.status).toBe(200);
    expect(calls).toHaveLength(1);
    expect(calls[0].init.credentials).toBe("same-origin");
    expect(requestCredentials("http://open.test")).toBe("same-origin");
  });

  test("asks for the token on a 401 and retries with credentials", async () => {
    const calls = mockFetch(new Response("Unauthorized", {status: 401}), json({ok: true}));
    let prompts = 0;
    setSignInHandler(async () => {
      prompts++;
      return true;
    });
    const response = await authFetch("http://locked.test", "http://locked.test/message", {method: "POST", body: "{}"});
    expect(response.status).toBe(200);
    expect(prompts).toBe(1);
    expect(calls).toHaveLength(2);
    expect(calls[1].init.method).toBe("POST");
    expect(calls[1].init.body).toBe("{}");
    expect(calls[1].init.credentials).toBe("include");
    expect(requestCredentials("http://locked.test")).toBe("include");
  });

  test("returns the 401 when the dialog is cancelled", async () => {
    mockFetch(new Response("Unauthorized", {status: 401}));
    setSignInHandler(async () => false);
    const response = await authFetch("http://cancel.test", "http://cancel.test/status");
    expect(response.status).toBe(401);
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
    const first = requestSignIn("http://shared.test");
    const second = requestSignIn("http://shared.test");
    finish(true);
    expect(await first).toBe(true);
    expect(await second).toBe(true);
    expect(prompts).toBe(1);
  });

  test("without a dialog nothing is asked", async () => {
    expect(await requestSignIn("http://nodialog.test")).toBe(false);
  });

  test("background checks stop asking after a cancel", async () => {
    let prompts = 0;
    setSignInHandler(async () => {
      prompts++;
      return false;
    });
    expect(await requestSignIn("http://bg.test")).toBe(false);
    expect(await requestSignIn("http://bg.test", {background: true})).toBe(false);
    expect(prompts).toBe(1);
    // Something the user did still asks.
    expect(await requestSignIn("http://bg.test")).toBe(false);
    expect(prompts).toBe(2);
  });
});

describe("ensureSession", () => {
  test("signed in or no auth: no prompt", async () => {
    let prompts = 0;
    setSignInHandler(async () => {
      prompts++;
      return true;
    });
    mockFetch(json({auth_required: false, authenticated: true}));
    expect(await ensureSession("http://noauth.test")).toBe(false);
    mockFetch(json({auth_required: true, authenticated: true}));
    expect(await ensureSession("http://signedin.test")).toBe(false);
    expect(prompts).toBe(0);
  });

  test("asks when the server needs a token", async () => {
    setSignInHandler(async () => true);
    mockFetch(json({auth_required: true, authenticated: false}));
    expect(await ensureSession("http://needs.test")).toBe(true);
    expect(isAuthRequired("http://needs.test")).toBe(true);
  });

  test("older servers without /auth/session", async () => {
    mockFetch(new Response("Not Found", {status: 404}));
    expect(await ensureSession("http://old.test")).toBe(false);
  });
});
