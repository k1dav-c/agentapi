// The hub's API for the changes its page makes: an instance's pin, group
// and mute, forgetting one, and the order of the pinned ones. Each change
// comes back on the event stream (/api/events), like any other; a call
// throws HubAPIError when the hub refuses it or doesn't answer.

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
  const call = async (path: string, init: RequestInit): Promise<void> => {
    let response: Response;
    try {
      response = await fetcher(`${root}${path}`, {credentials: "same-origin", ...init});
    } catch {
      throw new HubAPIError("The hub didn't answer.", 0);
    }
    if (response.ok) return;
    // The hub's own errors, or huma's.
    const body = (await response.json().catch(() => null)) as {error?: string; detail?: string; title?: string} | null;
    throw new HubAPIError(body?.error || body?.detail || body?.title || `The hub answered ${response.status}.`, response.status);
  };
  const json = (method: string, body: unknown): RequestInit => ({method, headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  const instance = (id: string) => `/api/instances/${encodeURIComponent(id)}`;
  return {
    // Pins, groups or mutes an instance (PATCH /api/instances/{id}).
    changeInstance: (id: string, change: InstanceChange) => call(instance(id), json("PATCH", change)),
    // Forgets an instance the hub doesn't follow now.
    forgetInstance: (id: string) => call(instance(id), {method: "DELETE"}),
    // The order of all the pinned instances (PUT /api/pins).
    setPins: (ids: string[]) => call("/api/pins", json("PUT", {ids})),
  };
}

export type HubAPI = ReturnType<typeof createHubAPI>;

// hubErrorText is what a toast says of error.
export function hubErrorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
