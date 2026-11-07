// A turn that failed on an upstream error which AgentAPI tries again
// (retry_wait in GET /status): how the chat words it.

import type {RetryWait} from "./chat-api";

// formatCountdown writes the time left as a clock does: 0:28, 4:05, 1:02:00.
export function formatCountdown(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const seconds = String(total % 60).padStart(2, "0");
  const minutes = Math.floor(total / 60) % 60;
  const hours = Math.floor(total / 3600);
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${seconds}` : `${minutes}:${seconds}`;
}

// What failed: the API's status when the agent says it.
export function retryTitle(wait: RetryWait): string {
  return wait.api_status ? `The API returned ${wait.api_status}` : "The API request failed";
}

// When AgentAPI tries again, and which try it is.
export function retryCountdown(wait: RetryWait, now: number): string {
  const left = Date.parse(wait.retry_at) - now;
  const which = `(${wait.attempt} of ${wait.attempts})`;
  if (!Number.isFinite(left) || left <= 0) return `Retrying now ${which}.`;
  return `Retrying in ${formatCountdown(left)} ${which}.`;
}
