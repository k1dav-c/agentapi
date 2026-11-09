// A fetch for tests: it answers from the responses given, in order, and
// records each call. restoreFetch puts the real one back (afterEach).

const realFetch = globalThis.fetch;

export interface Call {
  url: string;
  init: RequestInit;
}

export function mockFetch(...responses: Response[]): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
    calls.push({url: String(url), init});
    const next = responses.shift();
    if (!next) throw new Error(`unexpected fetch ${url}`);
    return next;
  }) as typeof fetch;
  return calls;
}

export function restoreFetch() {
  globalThis.fetch = realFetch;
}

// A JSON response.
export const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {status, headers: {"Content-Type": "application/json"}});

// routeFetch answers each call by its path (and method, "DELETE /api/x"),
// as often as asked, and records it; a path it has no answer for fails.
export function routeFetch(routes: Record<string, (call: Call) => Response | Promise<Response>>): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
    const call = {url: String(url), init};
    calls.push(call);
    const {pathname} = new URL(String(url), "http://hub.test");
    const answer = routes[`${init.method ?? "GET"} ${pathname}`] ?? routes[pathname];
    if (!answer) throw new Error(`unexpected fetch ${init.method ?? "GET"} ${url}`);
    return answer(call);
  }) as typeof fetch;
  return calls;
}
