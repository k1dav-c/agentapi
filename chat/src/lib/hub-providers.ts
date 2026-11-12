// Model providers are OpenAI- or Anthropic-style endpoints, such as a
// LiteLLM gateway, that Claude Code and Codex sessions can switch to. Those
// kept on the hub (GET and PUT /api/providers) reach every AgentAPI that
// reports to it; the hub's settings manage them. The hub never answers a
// key, only whether a provider has one.

import type {HubWire} from "./hub";
import {hubLimits} from "./hub-schema";

// A provider as the hub answers it: without its key.
export type Provider = HubWire["ProviderView"];

// A provider as PUT /api/providers takes it: without api_key the hub keeps
// the key the provider with the ID has, "" removes it.
export type ProviderChange = HubWire["ProviderChange"];

export type ProviderModel = HubWire["Model"];

// The most providers the hub keeps.
export const maxProviders = hubLimits.max_providers;

// ProviderDraft is what the editor edits. models is a text of model IDs,
// one per line or comma-separated. key empty keeps the key the provider
// has, unless removeKey.
export type ProviderDraft = {
  id: string;
  name: string;
  baseURL: string;
  anthropicBaseURL: string;
  models: string;
  key: string;
  removeKey: boolean;
};

// providerName is the name to show: the ID when it has none.
export function providerName(provider: {id: string; name?: string}): string {
  return provider.name?.trim() || provider.id;
}

// providerModelsText is what a list shows of a provider's models.
export function providerModelsText(provider: Provider): string {
  const count = provider.models?.length ?? 0;
  if (count === 0) return "lists its models";
  return count === 1 ? "1 model" : `${count} models`;
}

// parseProviders reads the providers of an answer: undefined when it isn't
// a list; entries that aren't providers are left out.
export function parseProviders(data: unknown): Provider[] | undefined {
  if (!Array.isArray(data)) return undefined;
  const providers: Provider[] = [];
  const ids = new Set<string>();
  for (const entry of data) {
    if (!entry || typeof entry !== "object") continue;
    const {id, name, base_url, anthropic_base_url, models, has_key} = entry as Partial<Record<keyof Provider, unknown>>;
    if (typeof id !== "string" || !id || ids.has(id) || typeof base_url !== "string") continue;
    const provider: Provider = {id, base_url, has_key: has_key === true};
    if (typeof name === "string" && name) provider.name = name;
    if (typeof anthropic_base_url === "string" && anthropic_base_url) provider.anthropic_base_url = anthropic_base_url;
    const kept = Array.isArray(models) ? models.flatMap(parseModel) : [];
    if (kept.length > 0) provider.models = kept;
    ids.add(id);
    providers.push(provider);
  }
  return providers;
}

function parseModel(entry: unknown): ProviderModel[] {
  if (!entry || typeof entry !== "object") return [];
  const {id, name} = entry as {id?: unknown; name?: unknown};
  if (typeof id !== "string" || !id) return [];
  return [typeof name === "string" && name ? {id, name} : {id}];
}

// draftOf is the editor's draft of a provider, or of a new one.
export function draftOf(provider?: Provider): ProviderDraft {
  return {
    id: provider?.id ?? "",
    name: provider?.name ?? "",
    baseURL: provider?.base_url ?? "",
    anthropicBaseURL: provider?.anthropic_base_url ?? "",
    models: (provider?.models ?? []).map((model) => model.id).join("\n"),
    key: "",
    removeKey: false,
  };
}

// parseModelIDs reads the models' text: one ID per line or comma-separated,
// each once.
export function parseModelIDs(text: string): string[] {
  const ids = text
    .split(/[\n,]/)
    .map((id) => id.trim())
    .filter(Boolean);
  return [...new Set(ids)];
}

// changeOf is the change that saves draft. A model that previous had keeps
// its name; the key is sent only when the draft sets or removes it.
export function changeOf(draft: ProviderDraft, previous?: Provider): ProviderChange {
  const change: ProviderChange = {id: draft.id.trim(), base_url: draft.baseURL.trim()};
  const name = draft.name.trim().replace(/\s+/g, " ");
  if (name) change.name = name;
  const anthropic = draft.anthropicBaseURL.trim();
  if (anthropic) change.anthropic_base_url = anthropic;
  const models = parseModelIDs(draft.models).map((id) => previous?.models?.find((model) => model.id === id) ?? {id});
  if (models.length > 0) change.models = models;
  if (draft.removeKey) change.api_key = "";
  else if (draft.key.trim()) change.api_key = draft.key.trim();
  return change;
}

// keptChange is the change that leaves provider as it is, key included.
export function keptChange(provider: Provider): ProviderChange {
  const change: ProviderChange & {has_key?: boolean} = {...provider};
  delete change.has_key;
  return change;
}

const idPattern = /^[a-z0-9][a-z0-9_-]{0,62}$/;

// draftProblem says what the hub would refuse in draft (lib/provider
// Validate), or undefined; others are the IDs of the other providers.
export function draftProblem(draft: ProviderDraft, others: readonly string[]): string | undefined {
  const id = draft.id.trim();
  if (!id) return "The ID is empty.";
  if (!idPattern.test(id)) return "The ID takes lowercase letters, digits, - and _, starts with a letter or digit, and has at most 63.";
  if (id === "default") return "The ID “default” is the agent's own provider.";
  if (others.includes(id)) return `Another provider has the ID “${id}”.`;
  if (!httpURL(draft.baseURL.trim())) return "The base URL must be an http or https URL.";
  const anthropic = draft.anthropicBaseURL.trim();
  if (anthropic && !httpURL(anthropic)) return "The Anthropic base URL must be an http or https URL.";
  return undefined;
}

// httpURL reports whether text is an http or https URL with a host.
function httpURL(text: string): boolean {
  if (!/^https?:\/\/[^/?#]/i.test(text)) return false;
  try {
    return new URL(text).host !== "";
  } catch {
    return false;
  }
}

// providerEndpoint is a provider's base URL as a list shows it: the host
// and path, without the scheme or the trailing slash.
export function providerEndpoint(baseURL: string): string {
  try {
    const url = new URL(baseURL);
    return url.host + url.pathname.replace(/\/+$/, "");
  } catch {
    return baseURL;
  }
}

// providerMonogram is the one or two letters of a provider's tile: the
// initials of its first two words, else its first two letters.
export function providerMonogram(name: string): string {
  const words = name.match(/[\p{L}\p{N}]+/gu) ?? [];
  if (words.length === 0) return "?";
  const [first, second] = words as [string, ...string[]];
  const letters = second ? first.charAt(0) + second.charAt(0) : first.slice(0, 2);
  return letters.toUpperCase();
}

// providerHue is the hue of a provider's tile, the same for an ID
// wherever it shows.
export function providerHue(id: string): number {
  let hash = 0;
  for (const char of id) hash = (hash * 31 + char.codePointAt(0)!) >>> 0;
  return hash % 360;
}

// toggleModel adds the model id to the models' text, or takes it out.
export function toggleModel(text: string, id: string): string {
  const ids = parseModelIDs(text);
  return (ids.includes(id) ? ids.filter((each) => each !== id) : [...ids, id]).join("\n");
}

// checkFailureText says in words why a connection check failed, from the
// hub's reason: a refused key, a URL without the API, a provider out of
// reach; else the reason itself.
export function checkFailureText(reason: string): string {
  const status = /answered (\d{3})/.exec(reason)?.[1];
  if (status === "401" || status === "403") return "The provider refused the key.";
  if (status === "404") return "No API at this URL: check that it ends with /v1.";
  if (status) return `The provider answered ${status}.`;
  if (/no such host|connection refused|i\/o timeout|deadline exceeded|unreachable|Client\.Timeout/i.test(reason)) return "The hub couldn't reach the provider.";
  if (/read the models/i.test(reason)) return "The URL answered, but not with a list of models.";
  return reason;
}
