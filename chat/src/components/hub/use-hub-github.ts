"use client";

import {useEffect, useSyncExternalStore} from "react";
import {gitHubStore} from "@/lib/hub-github-store";

// useHubGitHub is the hub's GitHub App, its status and repositories, as
// the Issues page and the settings' GitHub section share it: read while
// one of them shows, and again whenever the page shows again.
export function useHubGitHub(root: string) {
  const store = gitHubStore(root);
  const state = useSyncExternalStore(store.subscribe, store.get, store.get);
  useEffect(() => store.watch({window, document}), [store]);
  return {...state, read: store.read, disconnect: store.disconnect};
}
