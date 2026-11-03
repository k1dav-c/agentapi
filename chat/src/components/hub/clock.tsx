"use client";

import {createContext, useContext, useLayoutEffect, useState, type ReactNode} from "react";
import {Store, useStoreSelector} from "@/lib/store";
import {useNow} from "@/lib/use-elapsed";

// The hub's times (an age, how long a task runs) read the time from a
// clock of their own: it ticks every second while an agent works, and only
// what shows a time renders again, when what it shows changes. The page
// and the rows don't.
const HubClockContext = createContext<Store<number> | null>(null);

// HubClock ticks every second while fast (an agent works, for the running
// times), else every half minute (for the ages).
export function HubClock({fast, children}: {fast: boolean; children: ReactNode}) {
  const now = useNow(fast ? 1000 : 30_000);
  const [store] = useState(() => new Store(now));
  useLayoutEffect(() => store.set(now), [store, now]);
  return <HubClockContext.Provider value={store}>{children}</HubClockContext.Provider>;
}

// FixedHubClock stops the time at now, for tests.
export function FixedHubClock({now, children}: {now: number; children: ReactNode}) {
  const [store] = useState(() => new Store(now));
  return <HubClockContext.Provider value={store}>{children}</HubClockContext.Provider>;
}

const stopped = new Store(0);

// useHubTime returns select of the time now; the component renders again
// only when what it returns changes (isEqual). Outside a clock the time is 0.
export function useHubTime<S>(select: (now: number) => S, isEqual?: (a: S, b: S) => boolean): S {
  return useStoreSelector(useContext(HubClockContext) ?? stopped, select, isEqual);
}
