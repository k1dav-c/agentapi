// Signing the chat in to a server started with --api-token.
//
// Browsers can't add an Authorization header to EventSource or <img>
// requests, so the chat trades the token for an HttpOnly session cookie
// (POST /auth/session) and every request after that carries the cookie.
// The token comes from the URL fragment (`#token=...`, which never reaches
// a server, proxy or access log) or from the sign-in dialog shown when a
// request gets a 401.

interface AuthSessionState {
  auth_required: boolean;
  authenticated: boolean;
}

// Base URLs that answered 401 or said they require a token. Only their
// requests carry credentials across origins: a credentialed request to a
// server started with `--allowed-origins '*'` fails CORS, so requests to a
// server without auth stay as they were.
const authRequired = new Set<string>();

export function markAuthRequired(baseURL: string) {
  authRequired.add(baseURL);
}

export function isAuthRequired(baseURL: string) {
  return authRequired.has(baseURL);
}

export function isCrossOrigin(baseURL: string, origin = globalThis.location?.origin): boolean {
  if (!origin) return false;
  try {
    return new URL(baseURL, origin).origin !== origin;
  } catch {
    return false;
  }
}

// The server's host (and port), to say which server asks for the token.
export function serverHost(baseURL: string, origin = globalThis.location?.origin): string {
  try {
    return new URL(baseURL, origin).host || baseURL;
  } catch {
    return baseURL;
  }
}

// The credentials mode for a fetch to baseURL. Same-origin requests send
// the cookie either way.
export function requestCredentials(baseURL: string): RequestCredentials {
  return isAuthRequired(baseURL) ? "include" : "same-origin";
}

// Whether an EventSource (or XMLHttpRequest) to baseURL needs
// withCredentials to send the session cookie.
export function withCredentials(baseURL: string): boolean {
  return isAuthRequired(baseURL) && isCrossOrigin(baseURL);
}

// Splits `token=...` out of a URL fragment. Returns the token (or null) and
// the fragment without it, so other fragment parameters survive.
export function takeTokenFromHash(hash: string): {token: string | null; hash: string} {
  const raw = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!raw) return {token: null, hash};
  const params = new URLSearchParams(raw);
  const token = params.get("token");
  if (token === null) return {token: null, hash};
  params.delete("token");
  const rest = params.toString();
  return {token: token.trim() || null, hash: rest ? `#${rest}` : ""};
}

// What trading the token for a session cookie did. "not-required": the
// server needs no token at all. "cookie-blocked": the server took the
// token, but the browser didn't keep the cookie (blocked cookies, a Secure
// cookie on plain http), so requests still go out signed out.
export type SignInResult = "signed-in" | "not-required" | "rejected" | "cookie-blocked";

// Trades the API token for a session cookie (POST /auth/session), then
// checks that the browser kept the cookie.
export async function exchangeToken(baseURL: string, token: string): Promise<SignInResult> {
  const response = await fetch(`${baseURL}/auth/session`, {
    method: "POST",
    headers: {Authorization: `Bearer ${token}`},
    credentials: "include",
  });
  if (!response.ok) return "rejected";
  const state = (await response.json().catch(() => null)) as AuthSessionState | null;
  if (state && !state.auth_required) {
    // Without --api-token the server answers 200 and sets no cookie; its
    // requests go without credentials (see authRequired).
    authRequired.delete(baseURL);
    return "not-required";
  }
  markAuthRequired(baseURL);
  const check = await getAuthSession(baseURL);
  return check?.auth_required && !check.authenticated ? "cookie-blocked" : "signed-in";
}

// GET /auth/session. Null when the server doesn't have it (older servers)
// or can't be reached.
async function getAuthSession(baseURL: string): Promise<AuthSessionState | null> {
  const read = async (credentials: RequestCredentials) => {
    const response = await fetch(`${baseURL}/auth/session`, {credentials});
    return response.ok ? ((await response.json()) as AuthSessionState) : null;
  };
  try {
    let state = await read(requestCredentials(baseURL));
    if (state?.auth_required && !isAuthRequired(baseURL)) {
      markAuthRequired(baseURL);
      // Across origins the first request went without the cookie.
      if (!state.authenticated && isCrossOrigin(baseURL)) state = await read("include");
    }
    return state;
  } catch {
    return null;
  }
}

// Sign-in prompts. The dialog registers itself as the handler; requests
// that get a 401 wait for it. Concurrent 401s share one prompt.
type SignInHandler = (baseURL: string) => Promise<boolean>;

let signInHandler: SignInHandler | null = null;
let pendingSignIn: Promise<boolean> | null = null;
// Set when the user closes the dialog; background reconnects then stop
// asking until the user does something that needs the server.
let signInDismissed = false;

export function setSignInHandler(handler: SignInHandler | null) {
  signInHandler = handler;
}

export function requestSignIn(baseURL: string, {background = false} = {}): Promise<boolean> {
  if (!signInHandler || (background && signInDismissed)) return Promise.resolve(false);
  if (!pendingSignIn) {
    const handler = signInHandler;
    pendingSignIn = handler(baseURL)
      .then((ok) => {
        signInDismissed = !ok;
        return ok;
      })
      .finally(() => {
        pendingSignIn = null;
      });
  }
  return pendingSignIn;
}

// Sends a request to baseURL with the session cookie. On a 401 it is sent
// again: across origins first with credentials (the cookie may exist but
// wasn't sent), then once the user signed in. send gets the credentials
// mode to use; the status of what it returns tells a 401.
export async function withAuthRetry<T extends {status: number}>(
  baseURL: string,
  send: (credentials: RequestCredentials) => Promise<T>,
): Promise<T> {
  const result = await send(requestCredentials(baseURL));
  if (result.status !== 401) return result;
  if (!isAuthRequired(baseURL)) {
    markAuthRequired(baseURL);
    if (isCrossOrigin(baseURL)) {
      const retried = await send("include");
      if (retried.status !== 401) return retried;
    }
  }
  if (!(await requestSignIn(baseURL))) return result;
  return send("include");
}

// fetch for API calls; see withAuthRetry.
export function authFetch(baseURL: string, url: string, init: RequestInit = {}): Promise<Response> {
  return withAuthRetry(baseURL, (credentials) => fetch(url, {...init, credentials}));
}

// Runs once per server when the chat loads: signs in with a `#token=`
// fragment (and removes it from the address bar), then checks the session
// and asks for the token if the server needs one.
const bootstraps = new Map<string, Promise<void>>();

export function bootstrapSession(baseURL: string): Promise<void> {
  let bootstrap = bootstraps.get(baseURL);
  if (!bootstrap) {
    bootstrap = runBootstrap(baseURL).catch(() => undefined);
    bootstraps.set(baseURL, bootstrap);
  }
  return bootstrap;
}

async function runBootstrap(baseURL: string): Promise<void> {
  const location = globalThis.location;
  const {token, hash} = takeTokenFromHash(location?.hash ?? "");
  if (token) {
    history.replaceState(history.state, "", `${location.pathname}${location.search}${hash}`);
    try {
      const result = await exchangeToken(baseURL, token);
      if (result === "signed-in" || result === "not-required") return;
    } catch {
      // Fall through to the session check, which shows the dialog.
    }
  }
  await ensureSession(baseURL);
}

// Checks the session and, if the server needs a token this browser doesn't
// have, asks for it. Returns true when it just signed in, so a caller
// whose connection failed can retry right away. In the background (a
// reconnect nobody asked for) it doesn't ask again once the user closed the
// dialog.
export async function ensureSession(baseURL: string, {background = false} = {}): Promise<boolean> {
  const state = await getAuthSession(baseURL);
  if (!state || !state.auth_required || state.authenticated) return false;
  return requestSignIn(baseURL, {background});
}

// Forgets the servers, the dialog and the sign-ins above; for tests.
export function resetAuthState() {
  authRequired.clear();
  signInHandler = null;
  pendingSignIn = null;
  signInDismissed = false;
  bootstraps.clear();
}
