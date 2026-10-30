import {afterEach, describe, expect, test} from "bun:test";
import {createChatAPI, httpURL, mergeCost, type CostReport} from "./chat-api";
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
    const state = await createChatAPI(BASE).respondRequest("req-1", {option_id: "allow"});
    expect(state).toMatchObject({id: "req-1", status: "resolved"});
    expect(state).not.toHaveProperty("$schema");
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

describe("subagents", () => {
  test("a status a newer server added is unknown", async () => {
    const subagent = (id: string, status: string) => ({id, status, started_at: "2026-10-18T10:00:00Z", updated_at: "2026-10-18T10:01:00Z"});
    mockFetch(
      json({subagents: [subagent("a", "running"), subagent("b", "paused")]}),
      json({subagent: subagent("b", "paused"), prompt: "look", messages: null}),
    );
    const api = createChatAPI(BASE);
    expect((await api.listSubagents()).map((s) => s.status)).toEqual(["running", "unknown"]);
    expect((await api.getSubagent("b")).subagent.status).toBe("unknown");
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

describe("files", () => {
  test("getFileContent asks for the path and reads the file", async () => {
    const calls = mockFetch(json({
      $schema: "http://agentapi.test/schemas/FileContentResponseBody.json",
      path: "/work/lib/a b.go",
      relative: "lib/a b.go",
      size: 12,
      content: "package lib\n",
      truncated: false,
      binary: false,
    }));
    expect(await createChatAPI(BASE).getFileContent("/work/lib/a b.go")).toEqual({
      path: "/work/lib/a b.go",
      relative: "lib/a b.go",
      size: 12,
      content: "package lib\n",
      truncated: false,
      binary: false,
    });
    expect(calls[0].url).toBe(`${BASE}/files/content?path=%2Fwork%2Flib%2Fa%20b.go`);
    expect(calls[0].init.method).toBe("GET");
  });

  test("getFileDiff reads the status and the diff", async () => {
    const calls = mockFetch(json({path: "calc.py", relative: "calc.py", status: "modified", diff: "--- a/calc.py\n+++ b/calc.py\n", truncated: false}));
    expect(await createChatAPI(BASE).getFileDiff("calc.py")).toEqual({
      path: "calc.py",
      relative: "calc.py",
      status: "modified",
      diff: "--- a/calc.py\n+++ b/calc.py\n",
      truncated: false,
    });
    expect(calls[0].url).toBe(`${BASE}/files/diff?path=calc.py`);
  });

  test("a path outside the agent's working directory or a missing file is an ApiError with the status", async () => {
    mockFetch(json({detail: "/etc/passwd is outside the agent's working directory"}, 403), json({detail: "no such file"}, 404));
    const api = createChatAPI(BASE);
    expect(await failure(api.getFileContent("/etc/passwd"))).toMatchObject({status: 403, message: "/etc/passwd is outside the agent's working directory"});
    expect(await failure(api.getFileDiff("gone.txt"))).toMatchObject({status: 404, message: "no such file"});
  });
});

const call = (usd: number) => ({time: "2026-10-27T00:00:00Z", model: "m", tokens: {input: 1, cache_read: 0, cache_write: 0, output: 1}, usd, priced: true});
const report = (overrides: Partial<CostReport>): CostReport =>
  ({agent_session_id: "s", cost: {usd: 0, tokens: {input: 0, cache_read: 0, cache_write: 0, output: 0}, calls: 0, models: [], hourly: []}, calls: [], from: 0, total_calls: 0, digest: "", ...overrides}) as CostReport;

describe("mergeCost", () => {
  test("puts the calls made since after the known ones", () => {
    const known = report({calls: [call(1), call(2)], total_calls: 2, digest: "d2"});
    const merged = mergeCost(known, report({calls: [call(3)], from: 2, total_calls: 3, digest: "d3"}));
    expect(merged.calls.map((c) => c.usd)).toEqual([1, 2, 3]);
    expect(merged.total_calls).toBe(3);
    expect(merged.from).toBe(0);
  });
  test("takes a whole report as it is, or one of another session", () => {
    const known = report({calls: [call(1)], total_calls: 1, digest: "d1"});
    expect(mergeCost(known, report({calls: [call(5), call(6)], from: 0, total_calls: 2})).calls.map((c) => c.usd)).toEqual([5, 6]);
    expect(mergeCost(known, report({agent_session_id: "other", calls: [call(7)], from: 1, total_calls: 2})).calls.map((c) => c.usd)).toEqual([7]);
    expect(mergeCost(null, report({calls: [call(8)], total_calls: 1})).calls.map((c) => c.usd)).toEqual([8]);
  });
});
