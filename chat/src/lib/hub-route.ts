// Where the hub's page is, as the browser's history has it: the agent open
// beside the list (the address's #id), and whether the Issues page is open
// (an agent opened from it keeps it open below).
//
// Each agent opened is a page of its own in the history, so Back (a
// phone's back gesture) returns to the agent before, and from the first to
// the list or to Issues. Closing an agent returns there at once, past them
// all; home returns to the list and its overview, past Issues too. Issues
// is a page of its own: Back closes it. The address stays
// the list's while Issues is open, so the agents' (#id) keep working; a
// reload keeps what was open.

export interface HubRoute {
  // The agent open beside the list, if any.
  agent: string | undefined;
  // The Issues page is open: shown, or below the agent.
  issues: boolean;
}

// The pages of the history the hub pushes.
type HubEntry = {hub: "issues"} | {hub: "agent"; depth: number; issues?: true};

export interface RouteWindow {
  history: Pick<History, "state" | "pushState" | "replaceState" | "go" | "back">;
  location: Pick<Location, "hash" | "pathname" | "search">;
}

function hubOf(state: unknown): unknown {
  return typeof state === "object" && state !== null ? (state as {hub?: unknown}).hub : undefined;
}

// agentDepth is how many agents opened from the hub lead to the page of the
// browser's history in state: 0 for the list or Issues. A page an older
// hub opened an agent in says no depth; it is one.
export function agentDepth(state: unknown): number {
  if (hubOf(state) !== "agent") return 0;
  const depth = (state as {depth?: unknown}).depth;
  return typeof depth === "number" && Number.isInteger(depth) && depth > 0 ? depth : 1;
}

// routeOf is the route of a page of the history: its state, and its
// address's hash.
export function routeOf(state: unknown, hash: string): HubRoute {
  let agent: string | undefined;
  try {
    agent = decodeURIComponent(hash.slice(1)) || undefined;
  } catch {
    agent = hash.slice(1) || undefined;
  }
  const hub = hubOf(state);
  return {agent, issues: hub === "issues" || (hub === "agent" && (state as {issues?: unknown}).issues === true)};
}

export interface HubRouter {
  get: () => HubRoute;
  subscribe: (listener: () => void) => () => void;
  // select opens an agent, or closes the one open (undefined).
  select: (id: string | undefined) => void;
  // openIssues shows Issues: from an agent opened from it, the page below;
  // from another, past the agents first.
  openIssues: () => void;
  // closeIssues closes Issues when it shows (not below an agent).
  closeIssues: () => void;
  // toggleIssues is the list header's Issues: closes it when it shows,
  // shows it otherwise.
  toggleIssues: () => void;
  // home returns to the list and its overview: past the agents open and
  // Issues below them.
  home: () => void;
  // popped is told of each popstate: Back or Forward, or a go of the
  // router's own.
  popped: () => void;
}

export function createHubRouter(win: RouteWindow): HubRouter {
  const read = () => routeOf(win.history.state, win.location.hash);
  let route = read();
  const listeners = new Set<() => void>();
  const update = () => {
    const next = read();
    if (next.agent === route.agent && next.issues === route.issues) return;
    route = next;
    for (const listener of listeners) listener();
  };
  const here = () => win.location.pathname + win.location.search;
  const push = (entry: HubEntry, url?: string) => {
    win.history.pushState(entry, "", url);
    update();
  };
  // closing: a go past the agents is on its way to the page below them;
  // then is what to do there.
  let closing = false;
  let then: (() => void) | undefined;
  const pastAgents = (depth: number, next?: () => void) => {
    closing = true;
    then = next;
    win.history.go(-depth);
  };
  const showIssues = () => push({hub: "issues"});

  const select = (id: string | undefined) => {
    const depth = agentDepth(win.history.state);
    if (id) {
      if (route.agent === id) return;
      push({hub: "agent", depth: depth + 1, ...(route.issues && {issues: true as const})}, `#${encodeURIComponent(id)}`);
    } else if (!route.agent) {
      return;
    } else if (depth > 0) {
      pastAgents(depth);
    } else {
      // Opened at the agent's address (a bookmark, a reload of an older
      // hub's page): the list replaces it.
      win.history.replaceState(null, "", here());
      update();
    }
  };

  const openIssues = () => {
    if (!route.agent) {
      if (!route.issues) showIssues();
      return;
    }
    if (route.issues) return select(undefined);
    const depth = agentDepth(win.history.state);
    if (depth > 0) return pastAgents(depth, showIssues);
    win.history.replaceState(null, "", here());
    showIssues();
  };

  const closeIssues = () => {
    if (route.issues && !route.agent) win.history.back();
  };

  const home = () => {
    const depth = agentDepth(win.history.state);
    if (route.agent && depth === 0) {
      // Opened at the agent's address: the list replaces it.
      win.history.replaceState(null, "", here());
      update();
      return;
    }
    // Issues, shown or below the agents, is one page more.
    const pages = depth + (route.issues ? 1 : 0);
    if (pages > 0) pastAgents(pages);
  };

  return {
    get: () => route,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    select,
    openIssues,
    closeIssues,
    toggleIssues: () => (route.issues && !route.agent ? closeIssues() : openIssues()),
    home,
    popped: () => {
      if (closing) {
        closing = false;
        // A page opened at an agent's address has it in the page the close
        // lands on: the list replaces it.
        if (win.location.hash) win.history.replaceState(win.history.state, "", here());
      }
      update();
      const next = then;
      then = undefined;
      next?.();
    },
  };
}
