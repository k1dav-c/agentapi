import {useEffect, useRef, useState} from "react";

// How long to wait before showing a new value, so that values are shown at
// most once every `interval` ms. 0 means show it now.
export function throttleDelay(lastShownAt: number, now: number, interval: number): number {
  return Math.max(0, lastShownAt + interval - now);
}

// Returns value, updated at most once every `interval` ms. The latest value
// is always shown in the end (a trailing update), so nothing is lost; only
// the in-between frames are skipped.
export function useThrottledValue<T>(value: T, interval: number): T {
  const [shown, setShown] = useState(value);
  const lastShownAt = useRef(0);
  useEffect(() => {
    const timer = window.setTimeout(() => {
      lastShownAt.current = Date.now();
      setShown(value);
    }, throttleDelay(lastShownAt.current, Date.now(), interval));
    return () => window.clearTimeout(timer);
  }, [value, interval]);
  return shown;
}
