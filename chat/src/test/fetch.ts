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
