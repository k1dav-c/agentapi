// The hub's API for the changes its page makes: an instance's pin, group
// and mute, forgetting one, the order of the pinned ones, and the
// favorites. Each change comes back on the event stream (/api/events),
// like any other; a call throws HubAPIError when the hub refuses it or
// doesn't answer.

import {parseFavorites, type Favorite, type FavoriteDraft} from "./hub-favorites";
import type {Fetcher} from "./hub-update";

// HubAPIError is a call the hub refused (status is its answer's) or didn't
// answer (status 0); the message says which, in words for a toast.
export class HubAPIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "HubAPIError";
  }
}

export interface InstanceChange {
  pinned?: boolean;
  group?: string;
  muted?: boolean;
}

export function createHubAPI(root: string, fetcher: Fetcher = (url, init) => fetch(url, init)) {
  const call = async (path: string, init: RequestInit): Promise<Response | undefined> => {
    let response: Response;
    try {
      response = await fetcher(`${root}${path}`, {credentials: "same-origin", ...init});
    } catch {
      throw new HubAPIError("The hub didn't answer.", 0);
    }
    if (response.ok) return response;
    // The hub's own errors, or huma's.
    const body = (await response.json().catch(() => null)) as {error?: string; detail?: string; title?: string} | null;
    throw new HubAPIError(body?.error || body?.detail || body?.title || `The hub answered ${response.status}.`, response.status);
  };
  const json = (method: string, body: unknown): RequestInit => ({method, headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  const instance = (id: string) => `/api/instances/${encodeURIComponent(id)}`;
  const done = async (answer: Promise<unknown>): Promise<void> => {
    await answer;
  };
  return {
    // Pins, groups or mutes an instance (PATCH /api/instances/{id}).
    changeInstance: (id: string, change: InstanceChange) => done(call(instance(id), json("PATCH", change))),
    // Forgets an instance the hub doesn't follow now.
    forgetInstance: (id: string) => done(call(instance(id), {method: "DELETE"})),
    // The order of all the pinned instances (PUT /api/pins).
    setPins: (ids: string[]) => done(call("/api/pins", json("PUT", {ids}))),
    // Replaces the favorites (PUT /api/favorites), and returns them as the
    // hub keeps them: those sent without an ID have one.
    setFavorites: async (favorites: FavoriteDraft[]): Promise<Favorite[]> => {
      const response = await call("/api/favorites", json("PUT", {favorites}));
      const body = (await response?.json().catch(() => null)) as {favorites?: unknown} | null;
      const kept = parseFavorites(body?.favorites);
      if (!kept) throw new HubAPIError("The hub's answer has no favorites.", response?.status ?? 0);
      return kept;
    },
  };
}

// hubErrorText is what a toast says of error.
export function hubErrorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
