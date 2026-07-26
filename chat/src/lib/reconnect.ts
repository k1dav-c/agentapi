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
