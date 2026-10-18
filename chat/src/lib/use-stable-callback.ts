import {useCallback, useInsertionEffect, useRef} from "react";

// useStableCallback returns a function that keeps its identity from render
// to render and calls the latest fn: a handler a memoized component can take
// without rendering again every time its parent does. Not for calling during
// render.
export function useStableCallback<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
  const latest = useRef(fn);
  useInsertionEffect(() => {
    latest.current = fn;
  });
  return useCallback((...args: A) => latest.current(...args), []);
}
