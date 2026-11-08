import {useCallback, useRef, useState} from "react";

// A resource as the last read left it: on its way, read, or failed. While
// a read is on its way, and after it failed, data is what the read before
// it returned, for views that keep showing it.
export type Loaded<T> =
  | {state: "loading"; data?: T}
  | {state: "ready"; data: T}
  | {state: "error"; error: unknown; data?: T};

// useLoaded returns a resource read when load is called (again: read
// again), null until then; and set, which puts data in place of what was
// read, as an answer that returns it does. A read that ends after a newer
// one started is dropped.
export function useLoaded<T>(read: () => Promise<T>) {
  const [loaded, setLoaded] = useState<Loaded<T> | null>(null);
  const latest = useRef(0);
  const load = useCallback(() => {
    const id = ++latest.current;
    setLoaded((previous) => ({state: "loading", data: previous?.data}));
    read().then(
      (data) => {
        if (latest.current === id) setLoaded({state: "ready", data});
      },
      (error: unknown) => {
        if (latest.current === id) setLoaded((previous) => ({state: "error", error, data: previous?.data}));
      },
    );
  }, [read]);
  const set = useCallback((data: T) => setLoaded((previous) => (previous && previous.state !== "ready" ? {...previous, data} : {state: "ready", data})), []);
  return [loaded, load, set] as const;
}
