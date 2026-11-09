// What the hub page keeps in the browser, one key each: which sections are
// folded, whether to notify, the agents the list is filtered by, the tab
// picked on each machine's card, the list's order, By machine, and the
// list's width. Each setting reads what the page stored before it (a value
// it can't read is its default) and writes in the same format, so a page
// of another release reads it too. Where storage is unavailable (a private
// window, a sandboxed embed) the settings live in memory.

import {useCallback, useState} from "react";
import {parseAgents, type HubAgent} from "./hub-filter";
import {parseListWidth} from "./hub-list-width";
import {parseListOrder, type ListOrder} from "./hub-order";

// A tab picked on a machine's card: the agent, and when (milliseconds
// since the epoch); a pick from before the page kept when has none.
export type TabPick = {id: string; at?: number};

// The most cards whose pick the browser keeps; the oldest go first.
export const MAX_TAB_PICKS = 200;

type Setting<T> = {
  key: string;
  // parse reads what is stored, null for nothing; it never throws.
  parse: (stored: string | null) => T;
  serialize: (value: T) => string;
};

// fromJSON reads a JSON value with read, which gets undefined for nothing
// or what doesn't parse.
function fromJSON<T>(read: (value: unknown) => T): (stored: string | null) => T {
  return (stored) => {
    if (stored === null) return read(undefined);
    try {
      return read(JSON.parse(stored));
    } catch {
      return read(undefined);
    }
  };
}

function stringSet(value: unknown): Set<string> {
  return new Set(Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []);
}

// parseTabPicks reads the picks, the latest last: [card, id] or [card, id,
// at] each.
function parseTabPicks(value: unknown): Map<string, TabPick> {
  const picks = new Map<string, TabPick>();
  if (!Array.isArray(value)) return picks;
  for (const entry of value) {
    if (!Array.isArray(entry) || typeof entry[0] !== "string" || typeof entry[1] !== "string") continue;
    picks.set(entry[0], {id: entry[1], at: typeof entry[2] === "number" ? entry[2] : undefined});
  }
  return picks;
}

function serializeTabPicks(picks: ReadonlyMap<string, TabPick>): string {
  return JSON.stringify([...picks].map(([card, {id, at}]) => (at === undefined ? [card, id] : [card, id, at])));
}

// HubSettings are the settings by name, with their values.
export type HubSettings = {
  collapsed: ReadonlySet<string>;
  notify: boolean;
  agents: ReadonlySet<HubAgent>;
  tabPicks: ReadonlyMap<string, TabPick>;
  listOrder: ListOrder;
  byMachine: boolean;
  listWidth: number;
  issuesRepo: string;
  issuesAgent: string;
};
export type HubSettingName = keyof HubSettings;

export const hubSettings: {readonly [N in HubSettingName]: Setting<HubSettings[N]>} = {
  // The keys of the folded sections (ListSection.key), as JSON.
  collapsed: {key: "agentapi.hub.collapsed", parse: fromJSON(stringSet), serialize: (value) => JSON.stringify([...value])},
  // Whether to notify, as JSON.
  notify: {key: "agentapi.hub.notify", parse: fromJSON((value) => value === true), serialize: (value) => JSON.stringify(value)},
  // The agents picked, as JSON; none is every agent.
  agents: {key: "agentapi.hub.agents", parse: fromJSON(parseAgents), serialize: (value) => JSON.stringify([...value])},
  // The tab picked on each card, by the card, as JSON.
  tabPicks: {key: "agentapi.hub.machine-tabs", parse: fromJSON(parseTabPicks), serialize: serializeTabPicks},
  // The list's order, as its name.
  listOrder: {key: "agentapi.hub.list-order", parse: parseListOrder, serialize: (value) => value},
  // By machine, as "true" or "false".
  byMachine: {key: "agentapi.hub.by-machine", parse: (stored) => stored === "true", serialize: String},
  // The list's width in pixels, as a number; clamped to the window where
  // it is used.
  listWidth: {key: "agentapi.hub.list-width", parse: parseListWidth, serialize: String},
  // The repository the Issues view shows, as owner/name; "" is all of them.
  issuesRepo: {key: "agentapi.hub.issues-repo", parse: (stored) => (stored && /^[\w.-]+\/[\w.-]+$/.test(stored) ? stored : ""), serialize: (value) => value},
  // The agent last picked by hand for an issue, which Assign suggests
  // where a machine runs it; "" for none.
  issuesAgent: {key: "agentapi.hub.issues-agent", parse: (stored) => (stored && /^[\w.-]+$/.test(stored) ? stored : ""), serialize: (value) => value},
};

// readHubSetting is what the browser keeps of a setting, or its default.
export function readHubSetting<N extends HubSettingName>(name: N): HubSettings[N] {
  const {key, parse}: Setting<HubSettings[N]> = hubSettings[name];
  let stored: string | null = null;
  try {
    stored = localStorage.getItem(key);
  } catch {
    // Storage is unavailable, or this is the server's render.
  }
  return parse(stored);
}

// writeHubSetting keeps a setting in the browser.
export function writeHubSetting<N extends HubSettingName>(name: N, value: HubSettings[N]) {
  const {key, serialize}: Setting<HubSettings[N]> = hubSettings[name];
  try {
    localStorage.setItem(key, serialize(value));
  } catch {
    // Kept in memory only.
  }
}

// useHubSetting is a setting the browser keeps, and a function that
// changes it there too: to a value, or with a function of the one before.
export function useHubSetting<N extends HubSettingName>(
  name: N,
): [HubSettings[N], (next: HubSettings[N] | ((previous: HubSettings[N]) => HubSettings[N])) => void] {
  const [value, setValue] = useState(() => readHubSetting(name));
  const set = useCallback(
    (next: HubSettings[N] | ((previous: HubSettings[N]) => HubSettings[N])) =>
      setValue((previous) => {
        const value = typeof next === "function" ? (next as (previous: HubSettings[N]) => HubSettings[N])(previous) : next;
        writeHubSetting(name, value);
        return value;
      }),
    [name],
  );
  return [value, set];
}
