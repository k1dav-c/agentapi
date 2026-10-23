const MAX_RECONNECT_DELAY = 30_000;

export function getReconnectDelay(
  attempt: number,
  randomValue = Math.random(),
) {
  const normalizedAttempt = Math.max(1, Math.floor(attempt));
  const baseDelay = Math.min(
    MAX_RECONNECT_DELAY,
    1000 * 2 ** (normalizedAttempt - 1),
  );
  const jitter = 0.8 + Math.min(1, Math.max(0, randomValue)) * 0.4;
  return Math.min(MAX_RECONNECT_DELAY, Math.round(baseDelay * jitter));
}

// How many times the chat reconnects at once in QUICK_RECONNECT_WINDOW_MS
// before it backs off.
const QUICK_RECONNECTS = 3;
const QUICK_RECONNECT_WINDOW_MS = 10_000;

// Whether to reconnect at once, without showing that the connection
// dropped, when the event stream ends. A stream that was open ended
// because the server closed it (it closes them all on a resync, for the
// clients to come back with ?since=) or the network dropped it: trying
// again at once costs nothing, and a failure shows then. A server that
// keeps closing streams right away gets the backoff. recent holds when the
// earlier quick reconnects were; it is updated.
export function reconnectAtOnce(wasOpen: boolean, recent: number[], now = Date.now()): boolean {
  while (recent.length > 0 && now - recent[0] >= QUICK_RECONNECT_WINDOW_MS) recent.shift();
  if (!wasOpen || recent.length >= QUICK_RECONNECTS) return false;
  recent.push(now);
  return true;
}
