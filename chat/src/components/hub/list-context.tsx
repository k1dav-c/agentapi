"use client";

import {createContext, useContext} from "react";
import type {RowJob} from "@/lib/hub-dispatch";

// RowActions are what a row does to its instance; the hub keeps the same
// object from render to render.
export interface RowActions {
  select: (id: string) => void;
  forget: (id: string) => void;
  group: (id: string, name: string) => void;
  newGroup: (id: string) => void;
  // Mutes what it waits for now, until that changes; false ends the mute.
  mute: (id: string, muted: boolean) => void;
  // Pins it below the pinned ones; false unpins it.
  pin: (id: string, pinned: boolean) => void;
  // Moves it up (-1) or down (1) among the pinned ones.
  move: (id: string, step: -1 | 1) => void;
  // Opens an issue (owner/repo#n) in Issues.
  openIssue: (key: string) => void;
}

// HubListContext is what every row and card of the hub's list shares: the
// hub's own version, which marks the older instances; its address, whose
// proxy takes an instance's update; the groups in use, for a row's menu;
// the start most machines' names share (hostPrefix), which the cards dim;
// the dispatch's jobs, which the rows of their agents name; and what the
// rows do. The hub keeps it the same object while none of
// them changes, so a row renders again only when its own props change
// (the rows and cards are memoized).
export interface HubList {
  hubVersion: string;
  root: string;
  groups: readonly string[];
  prefix: string;
  // jobs are the issue dispatch's jobs by their AgentAPI's ID, what the
  // rows of those agents show of them (rowJobs): they name their issue.
  jobs: ReadonlyMap<string, RowJob>;
  actions: RowActions;
}

const noAction = () => {};

export const HubListContext = createContext<HubList>({
  hubVersion: "",
  root: "",
  groups: [],
  prefix: "",
  jobs: new Map(),
  actions: {select: noAction, forget: noAction, group: noAction, newGroup: noAction, mute: noAction, pin: noAction, move: noAction, openIssue: noAction},
});

export function useHubList(): HubList {
  return useContext(HubListContext);
}

// PinPlace is a row's place in the pinned section: whether it is the first
// or the last there. pinPlace gives one object per place, so a row's props
// stay the same while its place does.
export type PinPlace = Readonly<{first: boolean; last: boolean}>;

const pinPlaces: Record<string, PinPlace> = {};

export function pinPlace(first: boolean, last: boolean): PinPlace {
  const key = `${first} ${last}`;
  return (pinPlaces[key] ??= Object.freeze({first, last}));
}
