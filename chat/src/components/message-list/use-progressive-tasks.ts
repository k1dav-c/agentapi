"use client";

import {useEffect, useState} from "react";

// useProgressiveTasks returns the keys of the older tasks rendered in full
// so far. A reload lands at the latest task, so only that one renders right
// away; older tasks show their prompt and fill in one at a time while the
// browser is idle, newest first: rendering every step of several long tasks
// at once froze slower devices for seconds. Nothing fills in while paused
// (search and filters render everything).
export function useProgressiveTasks(olderTaskKeys: readonly string[], paused: boolean): ReadonlySet<string> {
  const [rendered, setRendered] = useState<ReadonlySet<string>>(() => new Set());
  const keys = olderTaskKeys.join("\n");
  useEffect(() => {
    if (paused || keys === "") return;
    const next = keys.split("\n").reverse().find((key) => !rendered.has(key));
    if (next === undefined) return;
    const render = () => setRendered((previous) => new Set(previous).add(next));
    if (typeof window.requestIdleCallback === "function") {
      const handle = window.requestIdleCallback(render, {timeout: 500});
      return () => window.cancelIdleCallback(handle);
    }
    const handle = window.setTimeout(render, 50);
    return () => window.clearTimeout(handle);
  }, [paused, keys, rendered]);
  return rendered;
}
