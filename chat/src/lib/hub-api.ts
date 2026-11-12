// The hub's API for the changes its page makes: signing in, an instance
// added by its URL, its pin, group and mute, forgetting one or the stopped
// ones, the order of the pinned ones, the favorites, and the model
// providers. Each change but the providers' comes back on the event stream
// (/api/events), like any other; a call throws
// HubAPIError when the hub refuses it or doesn't answer.

import type {HubWire} from "./hub";
import {parseFavorites, type Favorite, type FavoriteDraft} from "./hub-favorites";
import {parseProviders, type Provider, type ProviderChange} from "./hub-providers";
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

// InstanceChange is what PATCH /api/instances/{id} sets; the rest is left
// as it is.
export type InstanceChange = HubWire["InstanceChange"];

// hubCall calls the hub's API at path, and returns its answer, or throws
// HubAPIError: the hub's error ({code, message, error}; older hubs
// answered {error} on their own routes) or huma's ({detail}, {title}).
export function hubCall(root: string, fetcher: Fetcher = (url, init) => fetch(url, init)) {
  return async (path: string, init: RequestInit = {}): Promise<Response> => {
    let response: Response;
    try {
      response = await fetcher(`${root}${path}`, {credentials: "same-origin", ...init});
    } catch {
      throw new HubAPIError("The hub didn't answer.", 0);
    }
    if (response.ok) return response;
    const body = (await response.json().catch(() => null)) as (Partial<HubWire["ErrorBody"]> & {detail?: string; title?: string}) | null;
    throw new HubAPIError(body?.error || body?.message || body?.detail || body?.title || `The hub answered ${response.status}.`, response.status);
  };
}

export function createHubAPI(root: string, fetcher: Fetcher = (url, init) => fetch(url, init)) {
  const call = hubCall(root, fetcher);
  const json = (method: string, body: unknown): RequestInit => ({method, headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  const instance = (id: string) => `/api/instances/${encodeURIComponent(id)}`;
  const done = async (answer: Promise<unknown>): Promise<void> => {
    await answer;
  };
  return {
    // Trades the hub's token for its session cookie (POST /api/session),
    // which the other calls need; this one doesn't.
    signIn: (token: string) => done(call("/api/session", json("POST", {token} satisfies HubWire["SignInBody"]))),
    // Adds an AgentAPI by its URL (POST /api/instances), and returns its ID.
    addInstance: async (body: HubWire["AddInstanceBody"]): Promise<string> => {
      const response = await call("/api/instances", json("POST", body));
      const added = (await response.json().catch(() => null)) as Partial<Record<keyof HubWire["Instance"], unknown>> | null;
      if (typeof added?.id !== "string" || !added.id) throw new HubAPIError("The hub's answer doesn't say which instance it added.", response.status);
      return added.id;
    },
    // Pins, groups or mutes an instance (PATCH /api/instances/{id}).
    changeInstance: (id: string, change: InstanceChange) => done(call(instance(id), json("PATCH", change))),
    // Forgets an instance the hub doesn't follow now.
    forgetInstance: (id: string) => done(call(instance(id), {method: "DELETE"})),
    // Forgets those of ids that are stopped (POST /api/instances/forget),
    // at once, and returns those the hub forgot: one that started again
    // since stays.
    forgetStopped: async (ids: string[]): Promise<string[]> => {
      const response = await call("/api/instances/forget", json("POST", {ids} satisfies HubWire["ForgetBody"]));
      const body = (await response?.json().catch(() => null)) as Partial<Record<keyof HubWire["ForgetBody"], unknown>> | null;
      if (!Array.isArray(body?.ids)) throw new HubAPIError("The hub's answer doesn't say which it forgot.", response?.status ?? 0);
      return body.ids.filter((id): id is string => typeof id === "string");
    },
    // The order of all the pinned instances (PUT /api/pins).
    setPins: (ids: string[]) => done(call("/api/pins", json("PUT", {ids} satisfies HubWire["PinsBody"]))),
    // Replaces the favorites (PUT /api/favorites), and returns them as the
    // hub keeps them: those sent without an ID have one.
    setFavorites: async (favorites: FavoriteDraft[]): Promise<Favorite[]> => {
      const response = await call("/api/favorites", json("PUT", {favorites}));
      const body = (await response?.json().catch(() => null)) as Partial<Record<keyof HubWire["FavoritesBody"], unknown>> | null;
      const kept = parseFavorites(body?.favorites);
      if (!kept) throw new HubAPIError("The hub's answer has no favorites.", response?.status ?? 0);
      return kept;
    },
    // The model providers (GET /api/providers), without their keys.
    getProviders: async (): Promise<Provider[]> => providersOf(await call("/api/providers")),
    // Replaces the model providers (PUT /api/providers), and returns them as
    // the hub keeps them.
    setProviders: async (providers: ProviderChange[]): Promise<Provider[]> =>
      providersOf(await call("/api/providers", json("PUT", {providers} satisfies HubWire["ProvidersChange"]))),
    // Asks a provider for its models from the hub (POST
    // /api/providers/check): without api_key, with the key the provider
    // with the ID has. A provider that doesn't answer throws its reason.
    checkProvider: async (check: HubWire["ProviderCheck"]): Promise<ProviderCheckResult> => {
      const response = await call("/api/providers/check", json("POST", check));
      const body = (await response.json().catch(() => null)) as Partial<ProviderCheckResult> | null;
      if (!Array.isArray(body?.models)) throw new HubAPIError("The hub's answer has no models.", response.status);
      return {models: body.models.filter((m) => typeof m?.id === "string"), latency_ms: Number(body.latency_ms) || 0};
    },
  };
}

export type ProviderCheckResult = HubWire["ProviderCheckResult"];

// providersOf reads the providers of an answer, or throws.
async function providersOf(response: Response): Promise<Provider[]> {
  const body = (await response.json().catch(() => null)) as Partial<Record<keyof HubWire["ProvidersBody"], unknown>> | null;
  const providers = parseProviders(body?.providers);
  if (!providers) throw new HubAPIError("The hub's answer has no providers.", response.status);
  return providers;
}

// hubErrorText is what a toast says of error.
export function hubErrorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
