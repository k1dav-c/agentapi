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
// How many servers they are remembered for: the hub's dispatch starts a
// server for every issue, each with its own URL. Past it, those of the
// server the user closed one of the longest ago are forgotten.
export const MAX_DISMISSED_SERVERS = 50;

// The storage the dismissed notifications are kept in: localStorage, which
// tests replace. key and length find the servers of a browser that kept
// them before their index.
type KeyValueStorage = Pick<Storage, "getItem" | "setItem" | "removeItem"> & Partial<Pick<Storage, "key" | "length">>;

function browserStorage(): KeyValueStorage | null {
  try {
    return window.localStorage;
  } catch {
    // A private window or a sandboxed embed: memory only.
    return null;
  }
}

const dismissedPrefix = "agentapi.chat.dismissed-errors:";
const dismissedKey = (agentAPIUrl: string) => `${dismissedPrefix}${agentAPIUrl}`;
// The servers with dismissed notifications, the latest closed last.
const serversKey = "agentapi.chat.dismissed-errors";

function readServers(storage: KeyValueStorage): string[] {
  try {
    const stored: unknown = JSON.parse(storage.getItem(serversKey) ?? "null");
    if (Array.isArray(stored)) return stored.filter((url): url is string => typeof url === "string");
  } catch {
    // Unreadable: found again below.
  }
  // None yet: the servers this browser kept notifications of before.
  const found: string[] = [];
  for (let i = 0; i < (storage.length ?? 0); i++) {
    const key = storage.key?.(i);
    if (key?.startsWith(dismissedPrefix)) found.push(key.slice(dismissedPrefix.length));
  }
  return found;
}

// keepServer puts agentAPIUrl last among the servers, and forgets the
// notifications of the oldest past MAX_DISMISSED_SERVERS.
function keepServer(agentAPIUrl: string, storage: KeyValueStorage) {
  const servers = readServers(storage).filter((url) => url !== agentAPIUrl);
  servers.push(agentAPIUrl);
  for (const url of servers.splice(0, Math.max(0, servers.length - MAX_DISMISSED_SERVERS))) storage.removeItem(dismissedKey(url));
  storage.setItem(serversKey, JSON.stringify(servers));
}

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
// MAX_DISMISSED of them, of the latest MAX_DISMISSED_SERVERS servers.
export function rememberDismissed(agentAPIUrl: string, key: string, storage: KeyValueStorage | null = browserStorage()) {
  if (!storage) return;
  const keys = [...dismissedErrors(agentAPIUrl, storage)].filter((stored) => stored !== key);
  keys.push(key);
  try {
    storage.setItem(dismissedKey(agentAPIUrl), JSON.stringify(keys.slice(-MAX_DISMISSED)));
    keepServer(agentAPIUrl, storage);
  } catch {
    // Full or unavailable: the notification comes back after a reload.
  }
}
