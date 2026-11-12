"use client";

// Whether the hub shows the chat in its frame (FrameBar takes it from its
// messages): the hub keeps the chats of the agents opened last loaded
// behind the open one, and a chat it keeps there doesn't mark its tasks
// seen or alert, as if its tab were in the background. A chat shows until
// the hub says otherwise; one without the hub always does.
let shown = true;
const listeners = new Set<() => void>();

export function setFrameShown(next: boolean) {
  if (next === shown) return;
  shown = next;
  for (const listener of listeners) listener();
}

// frameHidden is whether the hub keeps the chat out of sight.
export function frameHidden(): boolean {
  return !shown;
}

// pageHidden is whether nobody sees the chat: its tab is in the background,
// or the hub keeps it out of sight.
export function pageHidden(): boolean {
  return document.hidden || !shown;
}

// onPageVisibility calls listener when pageHidden may have changed; it
// returns what stops it.
export function onPageVisibility(listener: () => void): () => void {
  listeners.add(listener);
  document.addEventListener("visibilitychange", listener);
  return () => {
    listeners.delete(listener);
    document.removeEventListener("visibilitychange", listener);
  };
}
