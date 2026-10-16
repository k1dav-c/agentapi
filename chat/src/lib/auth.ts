// Signing the chat in to a server started with --api-token.
//
// Browsers can't add an Authorization header to EventSource or <img>
// requests, so the chat trades the token for an HttpOnly session cookie
// (POST /auth/session) and every request after that carries the cookie.
// The token comes from the URL fragment (`#token=...`, which never reaches
// a server, proxy or access log) or from the sign-in dialog shown when a
// request gets a 401.

export interface AuthSessionState {
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

// Trades the API token for a session cookie. Returns whether the server
// accepted it.
export async function exchangeToken(baseURL: string, token: string): Promise<boolean> {
  const response = await fetch(`${baseURL}/auth/session`, {
    method: "POST",
    headers: {Authorization: `Bearer ${token}`},
    credentials: "include",
  });
  if (response.ok) markAuthRequired(baseURL);
  return response.ok;
}

// GET /auth/session. Null when the server doesn't have it (older servers)
// or can't be reached.
async function getAuthSession(baseURL: string): Promise<AuthSessionState | null> {
  const read = async (credentials: RequestCredentials) => {
    const response = await fetch(`${baseURL}/auth/session`, {credentials});
    if (!response.ok) return null;
    return (await response.json()) as AuthSessionState;
  };
  try {
    let state = await read(requestCredentials(baseURL));
    if (state?.auth_required && !state.authenticated && !isAuthRequired(baseURL)) {
      // Across origins the first request went without the cookie.
      markAuthRequired(baseURL);
      if (isCrossOrigin(baseURL)) state = await read("include");
    }
    if (state?.auth_required) markAuthRequired(baseURL);
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

// fetch for API calls: sends the session cookie, and on a 401 asks for the
// token and retries once signed in.
export async function authFetch(baseURL: string, url: string, init: RequestInit = {}): Promise<Response> {
  const response = await fetch(url, {...init, credentials: requestCredentials(baseURL)});
  if (response.status !== 401) return response;
  if (!isAuthRequired(baseURL)) {
    markAuthRequired(baseURL);
    if (isCrossOrigin(baseURL)) {
      // The cookie may exist but wasn't sent across origins.
      const retried = await fetch(url, {...init, credentials: "include"});
      if (retried.status !== 401) return retried;
    }
  }
  if (!(await requestSignIn(baseURL))) return response;
  return fetch(url, {...init, credentials: "include"});
}

// Runs once per server when the chat loads: signs in with a `#token=` fragment (and
// removes it from the address bar), then checks the session and asks for
// the token if the server needs one.
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
  if (typeof window !== "undefined") {
    const {token, hash} = takeTokenFromHash(window.location.hash);
    if (token) {
      const {pathname, search} = window.location;
      window.history.replaceState(window.history.state, "", `${pathname}${search}${hash}`);
      try {
        if (await exchangeToken(baseURL, token)) return;
      } catch {
        // Fall through to the session check, which shows the dialog.
      }
    }
  }
  await ensureSession(baseURL);
}

// Checks the session and, if the server needs a token this browser doesn't
// have, asks for it. Returns true when it just signed in, so a caller
// whose connection failed can retry right away.
export async function ensureSession(baseURL: string, {background = false} = {}): Promise<boolean> {
  const state = await getAuthSession(baseURL);
  if (!state || !state.auth_required || state.authenticated) return false;
  return requestSignIn(baseURL, {background});
}
