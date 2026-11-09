import {describe, expect, test} from "bun:test";
import {agentDepth, createHubRouter, routeOf, type HubRouter, type RouteWindow} from "./hub-route";

describe("agentDepth", () => {
  test("the agents opened that lead to a page of the history", () => {
    expect(agentDepth(null)).toBe(0);
    expect(agentDepth({other: true})).toBe(0);
    expect(agentDepth({hub: "issues"})).toBe(0);
    expect(agentDepth({hub: "agent", depth: 3})).toBe(3);
    // An older hub's page of an agent.
    expect(agentDepth({hub: "agent"})).toBe(1);
    expect(agentDepth({hub: "agent", depth: 0})).toBe(1);
    expect(agentDepth({hub: "agent", depth: "2"})).toBe(1);
  });
});

describe("routeOf", () => {
  test("the agent the address names, and Issues open or below it", () => {
    expect(routeOf(null, "")).toEqual({agent: undefined, issues: false});
    expect(routeOf({hub: "issues"}, "")).toEqual({agent: undefined, issues: true});
    expect(routeOf({hub: "agent", depth: 1}, "#a%20b")).toEqual({agent: "a b", issues: false});
    expect(routeOf({hub: "agent", depth: 1, issues: true}, "#a")).toEqual({agent: "a", issues: true});
    // A bookmark, and an address that doesn't decode.
    expect(routeOf(null, "#a")).toEqual({agent: "a", issues: false});
    expect(routeOf(null, "#%E0")).toEqual({agent: "%E0", issues: false});
  });
});

// A browser's history: its pages, the one shown, and popstate after a go,
// as a browser fires it, once the call returned.
class FakeHistory {
  pages: {state: unknown; url: string}[];
  at = 0;
  private router?: HubRouter;

  constructor(url = "/hub/", state: unknown = null) {
    this.pages = [{state, url}];
  }

  get state() {
    return this.pages[this.at].state;
  }

  get url() {
    return this.pages[this.at].url;
  }

  // The page's window; open (again) is the page loaded (or reloaded).
  open(): HubRouter {
    const page = () => this.pages[this.at];
    const url = () => new URL(page().url, "http://hub.test");
    const win: RouteWindow = {
      history: {
        get state() {
          return page().state;
        },
        pushState: (state, _unused, url) => {
          this.pages = [...this.pages.slice(0, this.at + 1), {state, url: this.resolve(url)}];
          this.at++;
        },
        replaceState: (state, _unused, url) => {
          this.pages[this.at] = {state, url: this.resolve(url)};
        },
        go: (delta = 0) => this.go(delta),
        back: () => this.go(-1),
      },
      location: {
        get hash() {
          return url().hash;
        },
        get pathname() {
          return url().pathname;
        },
        get search() {
          return url().search;
        },
      },
    };
    this.router = createHubRouter(win);
    return this.router;
  }

  // go moves as Back and Forward do (a phone's back gesture, a long press on
  // Back); popstate comes after.
  go(delta: number) {
    const to = Math.min(this.pages.length - 1, Math.max(0, this.at + delta));
    if (to === this.at) return;
    this.at = to;
    queueMicrotask(() => this.router?.popped());
  }

  private resolve(url: string | URL | null | undefined): string {
    if (url === undefined || url === null || url === "") return this.url;
    const next = new URL(String(url), new URL(this.url, "http://hub.test"));
    return next.pathname + next.search + next.hash;
  }
}

// Lets the popstate of a go land.
const popped = () => new Promise<void>((resolve) => queueMicrotask(resolve));

describe("the hub's route", () => {
  test("list → Issues → an agent → Back: Issues as it was", async () => {
    const history = new FakeHistory();
    const router = history.open();
    router.openIssues();
    expect(router.get()).toEqual({agent: undefined, issues: true});
    router.select("a");
    expect(router.get()).toEqual({agent: "a", issues: true});
    expect(history.url).toBe("/hub/#a");
    history.go(-1);
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: true});
    history.go(-1);
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: false});
  });

  test("Esc from agents opened from Issues: back to Issues, past them all", async () => {
    const history = new FakeHistory();
    const router = history.open();
    router.openIssues();
    router.select("a");
    // Another agent of its machine, from the bar's switch.
    router.select("b");
    expect(history.state).toEqual({hub: "agent", depth: 2, issues: true});
    router.select(undefined);
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: true});
    expect(history.at).toBe(1);
    expect(history.url).toBe("/hub/");
    // Esc again closes Issues.
    router.closeIssues();
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: false});
  });

  test("i from an agent opened from the list: past the agents, then Issues on top", async () => {
    const history = new FakeHistory();
    const router = history.open();
    router.select("a");
    router.select("b");
    router.openIssues();
    // On its way back past the agents.
    expect(router.get().agent).toBe("b");
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: true});
    expect(history.pages.map((page) => page.state)).toEqual([null, {hub: "issues"}]);
    // Back from Issues is the list.
    history.go(-1);
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: false});
  });

  test("i from an agent opened from Issues: the Issues below it; i on Issues does nothing", async () => {
    const history = new FakeHistory();
    const router = history.open();
    router.openIssues();
    router.select("a");
    router.openIssues();
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: true});
    router.openIssues();
    expect(history.pages).toHaveLength(3);
    expect(history.at).toBe(1);
  });

  test("the list header's Issues: opens it, closes it, and from an agent shows it", async () => {
    const history = new FakeHistory();
    const router = history.open();
    router.toggleIssues();
    expect(router.get().issues).toBe(true);
    router.toggleIssues();
    await popped();
    expect(router.get().issues).toBe(false);
    router.select("a");
    router.toggleIssues();
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: true});
  });

  test("a bookmark of an agent: closing it, or Issues, replaces it with the list", async () => {
    let history = new FakeHistory("/hub/#a");
    let router = history.open();
    expect(router.get()).toEqual({agent: "a", issues: false});
    router.select(undefined);
    expect(router.get()).toEqual({agent: undefined, issues: false});
    expect(history.pages).toEqual([{state: null, url: "/hub/"}]);

    history = new FakeHistory("/hub/#a");
    router = history.open();
    router.openIssues();
    expect(router.get()).toEqual({agent: undefined, issues: true});
    expect(history.pages).toEqual([
      {state: null, url: "/hub/"},
      {state: {hub: "issues"}, url: "/hub/"},
    ]);
  });

  test("a bookmark, then an agent opened, then closed: the list, without the bookmark's #id", async () => {
    const history = new FakeHistory("/hub/#a");
    const router = history.open();
    router.select("b");
    router.select(undefined);
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: false});
    expect(history.pages[0]).toEqual({state: null, url: "/hub/"});
  });

  test("a reload keeps what was open: Issues, and Issues below an agent", async () => {
    const history = new FakeHistory();
    let router = history.open();
    router.openIssues();
    expect(history.open().get()).toEqual({agent: undefined, issues: true});
    router = history.open();
    router.select("a");
    router = history.open();
    expect(router.get()).toEqual({agent: "a", issues: true});
    // Esc after the reload still returns to Issues.
    router.select(undefined);
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: true});
  });

  test("Forward past Issues into the agent opened from it: Issues is below it", async () => {
    const history = new FakeHistory();
    const router = history.open();
    router.openIssues();
    router.select("a");
    // A long press on Back, to the list; then on Forward, to the agent.
    history.go(-2);
    await popped();
    expect(router.get()).toEqual({agent: undefined, issues: false});
    history.go(2);
    await popped();
    expect(router.get()).toEqual({agent: "a", issues: true});
  });

  test("selecting the agent open already pushes no page; a change tells the subscribers once", () => {
    const history = new FakeHistory();
    const router = history.open();
    let told = 0;
    router.subscribe(() => told++);
    router.select("a");
    router.select("a");
    expect(history.pages).toHaveLength(2);
    expect(told).toBe(1);
  });
});
