"use client";

import {useCallback} from "react";
import {useSound, useSoundUnlock} from "@/lib/alert-sound";
import type {HubInstance} from "@/lib/hub";
import type {OpenTarget} from "@/lib/hub-push-browser";
import {useNeedsYou} from "./use-needs-you";
import {useNotify} from "./use-notify";
import {usePush, type HubPushState} from "./use-push";

// HubAlerts are the ways the hub's page tells you that an agent needs you
// while you look away, which the bell's menu turns on and off (AlertsMenu):
// a notification while the tab is hidden, a chime, and push to this device
// with the page closed (which stands in for the page's notification); and
// what a screen reader announces.
export interface HubAlerts {
  notify: boolean;
  toggleNotify: () => Promise<void>;
  sound: boolean;
  toggleSound: () => void;
  push: HubPushState;
  announcement: string;
}

// useHubAlerts is the page's alerts for the instances. open opens an agent
// or an issue (a notification clicked); needYou is how many agents need
// you, for the app icon's badge.
export function useHubAlerts({
  root,
  signedIn,
  instances,
  needYou,
  open,
}: {
  root: string;
  signedIn: boolean;
  instances: HubInstance[];
  needYou: number;
  open: (target: OpenTarget) => void;
}): HubAlerts {
  const {notify, toggleNotify} = useNotify();
  const {sound, toggleSound} = useSound();
  useSoundUnlock();
  const push = usePush(root, signedIn, open, needYou);
  const openAgent = useCallback((id: string) => open({agent: id}), [open]);
  const announcement = useNeedsYou(instances, notify && !push.push, openAgent);
  return {notify, toggleNotify, sound, toggleSound, push, announcement};
}
