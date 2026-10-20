// What the chat knows an agent_error event by, to show its toast once. The
// server sends its stored errors again on every connection, each with the
// time it happened. A warning shows once per page whenever it comes back:
// an agent repeats some on every start (Codex's note about a setting in its
// config file it ignores), and a restart would show them again.
export function agentErrorKey(error: {message: string; level: string; time: string}): string {
  return error.level === "warning" ? `warning\u0000${error.message}` : `${error.time}\u0000${error.message}`;
}

// How many dismissed notifications are remembered per server.
const MAX_DISMISSED = 200;

// The storage the dismissed notifications are kept in: localStorage, which
// tests replace.
type KeyValueStorage = Pick<Storage, "getItem" | "setItem">;

function browserStorage(): KeyValueStorage | null {
  try {
    return window.localStorage;
  } catch {
    // A private window or a sandboxed embed: memory only.
    return null;
  }
}

const dismissedKey = (agentAPIUrl: string) => `agentapi.chat.dismissed-errors:${agentAPIUrl}`;

// The notifications of the server at agentAPIUrl the user closed: the
// server replays its errors on every connection, so a reload would show
// them again.
export function dismissedErrors(agentAPIUrl: string, storage: KeyValueStorage | null = browserStorage()): Set<string> {
  try {
    const stored: unknown = JSON.parse(storage?.getItem(dismissedKey(agentAPIUrl)) ?? "[]");
    return new Set(Array.isArray(stored) ? stored.filter((key): key is string => typeof key === "string") : []);
  } catch {
    return new Set();
  }
}

// Remembers that the user closed the notification key, the latest
// MAX_DISMISSED of them.
export function rememberDismissed(agentAPIUrl: string, key: string, storage: KeyValueStorage | null = browserStorage()) {
  const keys = [...dismissedErrors(agentAPIUrl, storage)].filter((stored) => stored !== key);
  keys.push(key);
  try {
    storage?.setItem(dismissedKey(agentAPIUrl), JSON.stringify(keys.slice(-MAX_DISMISSED)));
  } catch {
    // Full or unavailable: the notification comes back after a reload.
  }
}
