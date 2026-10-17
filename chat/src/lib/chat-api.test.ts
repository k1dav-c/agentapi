import {afterEach, describe, expect, test} from "bun:test";
import {createChatAPI, httpURL} from "./chat-api";
import {resetAuthState, setSignInHandler} from "./auth";
import {ApiError} from "./error-utils";

const realFetch = globalThis.fetch;
const BASE = "http://agentapi.test";

interface Call {
  url: string;
  init: RequestInit;
}

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

// What a promise rejects with.
async function failure(promise: Promise<unknown>): Promise<unknown> {
  try {
    await promise;
  } catch (error) {
    return error;
  }
  throw new Error("expected a rejection");
}

afterEach(() => {
  globalThis.fetch = realFetch;
  resetAuthState();
});

describe("API errors", () => {
  test("carry the server's message and the status", async () => {
    mockFetch(json({detail: "request is resolved, not pending"}, 409));
    const error = await failure(createChatAPI(BASE).respondRequest("req-1", {option_id: "allow"}));
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({status: 409, message: "request is resolved, not pending"});
  });

  test("add huma's field errors to the detail", async () => {
    mockFetch(json({detail: "validation failed", errors: [{message: "ids: expected array"}]}, 422));
    expect(await failure(createChatAPI(BASE).reorderQueue([1]))).toMatchObject({
      status: 422,
      message: "validation failed: ids: expected array",
    });
  });

  test("fall back to a message of their own", async () => {
    mockFetch(new Response("Internal Server Error", {status: 500}));
    expect(await failure(createChatAPI(BASE).getRequests())).toMatchObject({
      status: 500,
      message: "Failed to load the agent's requests",
    });
  });

  test("a 401 left after a cancelled sign-in says so", async () => {
    setSignInHandler(async () => false);
    mockFetch(new Response("Unauthorized", {status: 401}));
    expect(await failure(createChatAPI(BASE).interrupt())).toMatchObject({
      status: 401,
      message: "Not signed in: this server needs its API token.",
    });
  });
});

describe("requests", () => {
  test("send JSON and read the answer", async () => {
    const calls = mockFetch(json({ok: true, queued: true}));
    expect(await createChatAPI(BASE).sendMessage("hi", "user", "steer")).toEqual({ok: true, queued: true, steered: false});
    expect(calls[0].url).toBe(`${BASE}/message`);
    expect(calls[0].init.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init.body))).toEqual({content: "hi", type: "user", dispatch_mode: "steer"});
    expect((calls[0].init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  test("queue mode leaves dispatch_mode out for older servers", async () => {
    const calls = mockFetch(json({ok: true}));
    await createChatAPI(BASE).sendMessage("hi", "user");
    expect(JSON.parse(String(calls[0].init.body))).toEqual({content: "hi", type: "user"});
  });

  test("an empty answer is fine", async () => {
    mockFetch(new Response(null, {status: 204}));
    await createChatAPI(BASE).interrupt();
  });

  test("an answered request comes back without huma's $schema", async () => {
    mockFetch(json({$schema: "http://agentapi.test/schemas/Request.json", id: "req-1", status: "resolved"}));
    expect(await createChatAPI(BASE).respondRequest("req-1", {option_id: "allow"})).toEqual({id: "req-1", status: "resolved"});
  });
});

describe("getStatus", () => {
  test("reads the transport and the login", async () => {
    mockFetch(json({transport: "native", login_required: {agent: "Codex", message: "Not logged in.", command: "codex login"}}));
    expect(await createChatAPI(BASE).getStatus()).toEqual({
      transport: "native",
      login_required: {agent: "Codex", message: "Not logged in.", command: "codex login", detail: undefined, host: undefined, user: undefined},
    });
  });

  test("older servers drive the agent through a terminal", async () => {
    mockFetch(json({status: "stable"}));
    expect(await createChatAPI(BASE).getStatus()).toEqual({transport: "pty", login_required: null});
  });
});

describe("retryLogin", () => {
  test("checking, or the agent no longer waits (409)", async () => {
    mockFetch(json({ok: true}), json({detail: "the agent is not waiting for a login"}, 409));
    const api = createChatAPI(BASE);
    expect(await api.retryLogin()).toBe("checking");
    expect(await api.retryLogin()).toBe("not-waiting");
  });

  test("other failures are errors", async () => {
    mockFetch(json({detail: "failed to check the login: boom"}, 500));
    expect(await failure(createChatAPI(BASE).retryLogin())).toMatchObject({status: 500, message: "failed to check the login: boom"});
  });
});

describe("getWorkspaceUrl", () => {
  test("only http(s) links", async () => {
    mockFetch(json({url: "https://coder.example.com/@dave/dev"}), json({url: "javascript:alert(1)"}), json({}));
    const api = createChatAPI(BASE);
    expect(await api.getWorkspaceUrl()).toBe("https://coder.example.com/@dave/dev");
    expect(await api.getWorkspaceUrl()).toBeUndefined();
    expect(await api.getWorkspaceUrl()).toBeUndefined();
  });
});

describe("httpURL", () => {
  test("keeps http(s) URLs, resolved against a base", () => {
    expect(httpURL("https://example.com/a")).toBe("https://example.com/a");
    expect(httpURL("/ws", "http://localhost:3284")).toBe("http://localhost:3284/ws");
  });

  test("drops anything else", () => {
    expect(httpURL("javascript:alert(1)")).toBeUndefined();
    expect(httpURL("data:text/html,hi")).toBeUndefined();
    expect(httpURL("")).toBeUndefined();
    expect(httpURL(undefined)).toBeUndefined();
    expect(httpURL("not a url")).toBeUndefined();
  });
});
