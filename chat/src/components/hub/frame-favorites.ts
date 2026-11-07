"use client";

import {useSyncExternalStore} from "react";
import type {Favorite} from "@/lib/hub-favorites";
import {fromFrame} from "@/lib/hub-frame";

// The favorites the hub told the chat in its frame of (FrameBar takes them
// from its messages), for the composer. undefined until the hub sends them:
// a chat opened without the hub, or by a hub before 3.5, has none, and the
// composer shows no favorites.
let favorites: Favorite[] | undefined;
const listeners = new Set<() => void>();

export function setFrameFavorites(next: Favorite[]) {
  favorites = next;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useFrameFavorites(): Favorite[] | undefined {
  return useSyncExternalStore(
    subscribe,
    () => favorites,
    () => undefined,
  );
}

// manageFavorites asks the hub to open its settings at the favorites.
export function manageFavorites() {
  window.parent.postMessage(fromFrame("manage-favorites"), window.location.origin);
}
