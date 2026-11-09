"use client";

import {useCallback, useEffect, useState} from "react";

// issuesEntry says whether a page of the browser's history is the Issues
// view's.
export function issuesEntry(state: unknown): boolean {
  return typeof state === "object" && state !== null && (state as {hub?: unknown}).hub === "issues";
}

// useIssuesRoute is whether the Issues view is open, as a page of the
// browser's history: Back (a phone's back gesture) closes it, and an agent
// opened from it goes back to it. The address stays the same, so the
// agents' (#id) keep working; a reload keeps the view open.
export function useIssuesRoute(): {open: boolean; show: (open: boolean) => void} {
  const [open, setOpen] = useState(() => typeof window !== "undefined" && issuesEntry(history.state));
  const show = useCallback((on: boolean) => {
    const here = issuesEntry(history.state);
    if (on) {
      if (!here) history.pushState({hub: "issues"}, "");
      setOpen(true);
    } else if (here) {
      history.back();
    } else {
      setOpen(false);
    }
  }, []);
  useEffect(() => {
    // An agent's page opened from the view leaves it open below.
    const onPop = () => setOpen(issuesEntry(history.state) || (open && !!history.state && (history.state as {hub?: unknown}).hub === "agent"));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, [open]);
  return {open, show};
}
