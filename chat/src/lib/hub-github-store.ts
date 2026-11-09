// The hub's GitHub App as the page knows it: its status and the
// repositories it is on, read once for the Issues page and the settings'
// GitHub section alike, again whenever the page shows again (back from
// GitHub, where they change), and after a disconnect. Each read stands for
// all that came before it: an earlier one that answers late is dropped.

import {createDispatchAPI, type GitHubRepo, type GitHubStatus} from "./hub-dispatch";
import {hubErrorText} from "./hub-api";
import type {Load} from "./load";

// The repositories the hub lists for the app: not asked (no installation),
// on their way, listed, or the hub couldn't.
export type ReposLoad = {state: "none"} | {state: "loading"} | {state: "ready"; value: GitHubRepo[]} | {state: "failed"; error: string};

export interface HubGitHub {
  status: Load<GitHubStatus>;
  repos: ReposLoad;
  // A read is under way.
  reading: boolean;
}

export type GitHubAPI = Pick<ReturnType<typeof createDispatchAPI>, "github" | "repos" | "disconnect">;

// Where the store hears that the page shows again.
export interface ShowEvents {
  window: Pick<Window, "addEventListener" | "removeEventListener">;
  document: Pick<Document, "addEventListener" | "removeEventListener" | "visibilityState">;
}

export interface GitHubStore {
  get: () => HubGitHub;
  subscribe: (listener: () => void) => () => void;
  // read reads the app again; it resolves to why it couldn't, if it
  // couldn't. Once read, a failure keeps what was read before.
  read: () => Promise<string | undefined>;
  // disconnect has the hub forget its app (it throws the hub's error), and
  // reads the app again.
  disconnect: () => Promise<void>;
  // watch reads the app, and again each time the page shows again, until
  // the function it returns is called (by every watch).
  watch: (events: ShowEvents) => () => void;
}

export function createGitHubStore(api: GitHubAPI): GitHubStore {
  let state: HubGitHub = {status: {state: "loading"}, repos: {state: "none"}, reading: false};
  const listeners = new Set<() => void>();
  const set = (next: Partial<HubGitHub>) => {
    state = {...state, ...next};
    for (const listener of listeners) listener();
  };
  // The latest read: an answer to any other is out of date.
  let latest = 0;
  let watchers = 0;
  let unlisten: (() => void) | undefined;

  const read = async (): Promise<string | undefined> => {
    const seq = ++latest;
    set({reading: true});
    try {
      const status = await api.github();
      if (seq !== latest) return undefined;
      if (!status.connected || (status.installations ?? []).length === 0) {
        set({status: {state: "ready", value: status}, repos: {state: "none"}, reading: false});
        return undefined;
      }
      set({status: {state: "ready", value: status}, repos: state.repos.state === "ready" ? state.repos : {state: "loading"}});
      let repos: ReposLoad;
      try {
        repos = {state: "ready", value: await api.repos()};
      } catch (error) {
        repos = {state: "failed", error: hubErrorText(error)};
      }
      if (seq === latest) set({repos, reading: false});
      return undefined;
    } catch (error) {
      if (seq !== latest) return undefined;
      const why = hubErrorText(error);
      set({status: state.status.state === "ready" ? state.status : {state: "failed", error: why}, reading: false});
      return why;
    }
  };

  return {
    get: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    read,
    disconnect: async () => {
      await api.disconnect();
      await read();
    },
    watch: (events) => {
      void read();
      if (watchers++ === 0) {
        const onShow = () => {
          if (events.document.visibilityState === "visible") void read();
        };
        events.window.addEventListener("focus", onShow);
        events.document.addEventListener("visibilitychange", onShow);
        unlisten = () => {
          events.window.removeEventListener("focus", onShow);
          events.document.removeEventListener("visibilitychange", onShow);
        };
      }
      let watching = true;
      return () => {
        if (!watching) return;
        watching = false;
        if (--watchers === 0) {
          unlisten?.();
          unlisten = undefined;
        }
      };
    },
  };
}

// The store of each hub the page talks to (one, at its root).
const stores = new Map<string, GitHubStore>();

export function gitHubStore(root: string): GitHubStore {
  let store = stores.get(root);
  if (!store) {
    store = createGitHubStore(createDispatchAPI(root));
    stores.set(root, store);
  }
  return store;
}
