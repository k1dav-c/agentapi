import {useCallback, useInsertionEffect, useRef, type RefObject} from "react";

// useLatest returns a ref that holds the value of the latest render, for
// the handlers and callbacks that outlive a render: a listener added once,
// a function that keeps its identity. The ref is set as the render
// commits, before any effect runs, and not during the render itself,
// which React may throw away. Not for reading during render.
export function useLatest<T>(value: T): RefObject<T> {
  const latest = useRef(value);
  useInsertionEffect(() => {
    latest.current = value;
  });
  return latest;
}

// useStableCallback returns a function that keeps its identity from render
// to render and calls the latest fn: a handler a memoized component can take
// without rendering again every time its parent does. Not for calling during
// render.
export function useStableCallback<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
  const latest = useLatest(fn);
  return useCallback((...args: A) => latest.current(...args), [latest]);
}
