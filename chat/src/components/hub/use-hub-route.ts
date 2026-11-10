"use client";

import {useEffect, useState, useSyncExternalStore} from "react";
import {createHubRouter, type HubRoute, type HubRouter, type RouteWindow} from "@/lib/hub-route";

// The page's static build has no history: nothing is open there.
const noWindow: RouteWindow = {
  history: {state: null, pushState: () => {}, replaceState: () => {}, go: () => {}, back: () => {}},
  location: {hash: "", pathname: "", search: ""},
};
const nowhere: HubRoute = {agent: undefined, issues: false};

// useHubRoute is where the hub's page is (lib/hub-route.ts): the agent open
// beside the list and whether Issues is open, with the ways to change
// them, as pages of the browser's history.
export function useHubRoute(): HubRoute & Omit<HubRouter, "get" | "subscribe" | "popped"> {
  const [router] = useState(() => createHubRouter(typeof window === "undefined" ? noWindow : window));
  const route = useSyncExternalStore(router.subscribe, router.get, () => nowhere);
  useEffect(() => {
    window.addEventListener("popstate", router.popped);
    return () => window.removeEventListener("popstate", router.popped);
  }, [router]);
  return {...route, select: router.select, openIssues: router.openIssues, closeIssues: router.closeIssues, toggleIssues: router.toggleIssues, home: router.home};
}
