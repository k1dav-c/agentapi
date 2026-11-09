import {describe, expect, test} from "bun:test";
import {HubAPIError} from "./hub-api";
import type {GitHubRepo, GitHubStatus} from "./hub-dispatch";
import {createGitHubStore, type GitHubAPI, type ShowEvents} from "./hub-github-store";

const installed: GitHubStatus = {connected: true, slug: "app", installations: [{id: 1, account: "acme", repository_selection: "all", avatar_url: ""}]};
const nowhere: GitHubStatus = {connected: true, slug: "app", installations: []};
const repo = (full_name: string) => ({full_name}) as GitHubRepo;

// A call answered when the test says.
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return {promise, resolve, reject};
}

// An API whose calls wait in line until answered.
function fakeAPI() {
  const github: ReturnType<typeof deferred<GitHubStatus>>[] = [];
  const repos: ReturnType<typeof deferred<GitHubRepo[]>>[] = [];
  const calls: string[] = [];
  const api: GitHubAPI = {
    github: () => {
      calls.push("github");
      const call = deferred<GitHubStatus>();
      github.push(call);
      return call.promise;
    },
    repos: () => {
      calls.push("repos");
      const call = deferred<GitHubRepo[]>();
      repos.push(call);
      return call.promise;
    },
    disconnect: async () => void calls.push("disconnect"),
  };
  return {api, github, repos, calls};
}

// The window and document the store listens to: the page shows again.
function fakeEvents() {
  const window = new EventTarget();
  const document = Object.assign(new EventTarget(), {visibilityState: "visible" as DocumentVisibilityState});
  const events: ShowEvents = {window, document};
  return {events, show: () => window.dispatchEvent(new Event("focus"))};
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("the hub's GitHub App, shared", () => {
  test("reads the app and, once installed, its repositories", async () => {
    const {api, github, repos} = fakeAPI();
    const store = createGitHubStore(api);
    expect(store.get()).toEqual({status: {state: "loading"}, repos: {state: "none"}, reading: false});
    const read = store.read();
    expect(store.get().reading).toBe(true);
    github[0].resolve(installed);
    await tick();
    expect(store.get().repos).toEqual({state: "loading"});
    repos[0].resolve([repo("acme/widget")]);
    await read;
    expect(store.get()).toEqual({status: {state: "ready", value: installed}, repos: {state: "ready", value: [repo("acme/widget")]}, reading: false});

    // Installed nowhere: no repositories to ask for.
    const again = store.read();
    github[1].resolve(nowhere);
    await again;
    expect(store.get().repos).toEqual({state: "none"});
    expect(repos).toHaveLength(1);
  });

  test("a read that answers after a later one is dropped", async () => {
    const {api, github} = fakeAPI();
    const store = createGitHubStore(api);
    const first = store.read();
    const second = store.read();
    github[1].resolve(nowhere);
    await second;
    github[0].resolve({connected: false, installations: []});
    await first;
    expect(store.get().status).toEqual({state: "ready", value: nowhere});
    expect(store.get().reading).toBe(false);
  });

  test("disconnect reads the app again, past a read under way", async () => {
    const {api, github, calls} = fakeAPI();
    const store = createGitHubStore(api);
    const slow = store.read();
    const done = store.disconnect();
    await tick();
    expect(calls).toEqual(["github", "disconnect", "github"]);
    github[1].resolve({connected: false, installations: []});
    await done;
    github[0].resolve(nowhere);
    await slow;
    expect(store.get().status).toEqual({state: "ready", value: {connected: false, installations: []}});
  });

  test("a read that fails says why; once read, what was read stays", async () => {
    const {api, github} = fakeAPI();
    const store = createGitHubStore(api);
    const first = store.read();
    github[0].reject(new HubAPIError("The hub didn't answer.", 0));
    expect(await first).toBe("The hub didn't answer.");
    expect(store.get().status).toEqual({state: "failed", error: "The hub didn't answer."});

    const second = store.read();
    github[1].resolve(nowhere);
    expect(await second).toBeUndefined();
    const third = store.read();
    github[2].reject(new HubAPIError("GitHub said no", 502));
    expect(await third).toBe("GitHub said no");
    expect(store.get().status).toEqual({state: "ready", value: nowhere});
  });

  test("watched, it reads at once and each time the page shows again, until the last watch ends", async () => {
    const {api, calls} = fakeAPI();
    const store = createGitHubStore(api);
    const {events, show} = fakeEvents();
    const issues = store.watch(events);
    const settings = store.watch(events);
    expect(calls).toEqual(["github", "github"]);
    show();
    expect(calls).toHaveLength(3);
    issues();
    issues();
    show();
    expect(calls).toHaveLength(4);
    settings();
    show();
    expect(calls).toHaveLength(4);
  });

  test("tells its subscribers of each change", async () => {
    const {api, github} = fakeAPI();
    const store = createGitHubStore(api);
    let told = 0;
    const unsubscribe = store.subscribe(() => told++);
    const read = store.read();
    github[0].resolve({connected: false, installations: []});
    await read;
    expect(told).toBe(2);
    unsubscribe();
    void store.read();
    expect(told).toBe(2);
  });
});
