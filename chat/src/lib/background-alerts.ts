import type { AgentStateKind } from "./session-status";

export type BackgroundAlert = "needs-you" | "finished";

// What to tell someone who isn't looking at the tab, given the state change.
// Only transitions matter: a tab opened on an agent that already waits
// shouldn't notify, nor should every status poll.
export function alertForTransition(previous: AgentStateKind, current: AgentStateKind): BackgroundAlert | null {
  if (current === "needs-you" && previous !== "needs-you") return "needs-you";
  if (current === "ready" && previous === "working") return "finished";
  return null;
}

export type FaviconBadge = "needs-you" | "working" | "finished" | null;

// The tab icon: the agent mark (as in the header) with a state dot, so a
// background tab shows at a glance whether the agent needs you.
export function faviconDataUrl(badge: FaviconBadge, markColor: string): string {
  const dot = {
    "needs-you": "#c27c0e",
    working: "#4f6bed",
    finished: "#16a07a",
  } as const;
  const badgeSvg = badge
    ? `<circle cx="25" cy="7" r="6.5" fill="${dot[badge]}" stroke="#ffffff" stroke-width="2"/>`
    : "";
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">` +
    `<rect x="1" y="1" width="30" height="30" rx="8" fill="#232733"/>` +
    `<rect x="10" y="10" width="12" height="12" rx="3" fill="${markColor}"/>` +
    badgeSvg +
    `</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

const PREFERENCE_KEY = "agentapi.chat.notifications";
const PREFERENCE_EVENT = "agentapi-notifications-change";

export function notificationsEnabled(): boolean {
  try {
    return window.localStorage.getItem(PREFERENCE_KEY) === "on";
  } catch {
    return false;
  }
}

export function setNotificationsEnabled(enabled: boolean) {
  try {
    if (enabled) window.localStorage.setItem(PREFERENCE_KEY, "on");
    else window.localStorage.removeItem(PREFERENCE_KEY);
  } catch {
    // Without storage the choice lasts for this page only.
  }
  window.dispatchEvent(new Event(PREFERENCE_EVENT));
}

export function onNotificationsPreferenceChange(listener: () => void): () => void {
  window.addEventListener(PREFERENCE_EVENT, listener);
  window.addEventListener("storage", listener);
  return () => {
    window.removeEventListener(PREFERENCE_EVENT, listener);
    window.removeEventListener("storage", listener);
  };
}
