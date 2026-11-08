"use client";

import {useCallback, useEffect, useRef, useState} from "react";

// agentDepth is how many agents opened from the hub lead to the page of the
// browser's history in state: 0 for the list. A page an older hub opened
// an agent in says no depth; it is one.
export function agentDepth(state: unknown): number {
  if (typeof state !== "object" || state === null || (state as {hub?: unknown}).hub !== "agent") return 0;
  const depth = (state as {depth?: unknown}).depth;
  return typeof depth === "number" && Number.isInteger(depth) && depth > 0 ? depth : 1;
}

// openInURL is the agent the page's address names (#id), if any.
function openInURL(): string | undefined {
  return decodeURIComponent(window.location.hash.slice(1)) || undefined;
}

// useHubHistory is the agent open beside the list, as the page's address
// names it, and select, which opens another (or none, the list).
//
// Each agent opened is a page of its own in the browser's history, so
// Back (a phone's back gesture) returns to the agent before, and from the
// first to the list. Closing returns to the list at once, past them all.
export function useHubHistory(): {selected: string | undefined; select: (id: string | undefined) => void} {
  const [selected, setSelected] = useState<string | undefined>(() => (typeof window === "undefined" ? undefined : openInURL()));
  // closing: Back past the agents opened is on its way to the list.
  const closing = useRef(false);
  const select = useCallback((id: string | undefined) => {
    const depth = agentDepth(history.state);
    if (id) {
      // The agent open already: no page again.
      if (openInURL() === id) {
        setSelected(id);
        return;
      }
      history.pushState({hub: "agent", depth: depth + 1}, "", `#${encodeURIComponent(id)}`);
    } else if (depth > 0) {
      closing.current = true;
      history.go(-depth);
      return;
    } else {
      history.replaceState(null, "", window.location.pathname + window.location.search);
    }
    setSelected(id);
  }, []);

  // Back and forward show what the URL names.
  useEffect(() => {
    const onPop = () => {
      // A page opened at an agent's address (a bookmark, a reload) has it
      // in the entry the close lands on: the list replaces it.
      if (closing.current) {
        closing.current = false;
        if (window.location.hash) history.replaceState(null, "", window.location.pathname + window.location.search);
        setSelected(undefined);
        return;
      }
      setSelected(openInURL());
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  return {selected, select};
}
