import {useEffect, useState} from "react";
import {useStableCallback} from "./use-stable-callback";

// usePersisted keeps value in localStorage under key. When key changes
// (and at first), restore gets what is stored there, or null; from then on
// the value is stored whenever it changes, and removed when serialize
// returns null. A restore that throws (what is stored is broken) removes
// it. Where storage is unavailable (a private window, a sandboxed embed)
// the value only lives in memory.
export function usePersisted<T>(
  key: string,
  value: T,
  serialize: (value: T) => string | null,
  restore: (stored: string | null) => void,
) {
  const [restoredKey, setRestoredKey] = useState<string | null>(null);
  const restoreLatest = useStableCallback(restore);
  const serializeLatest = useStableCallback(serialize);

  useEffect(() => {
    let stored: string | null = null;
    try {
      stored = window.localStorage.getItem(key);
    } catch {
      // Storage is unavailable.
    }
    try {
      restoreLatest(stored);
    } catch {
      try {
        window.localStorage.removeItem(key);
      } catch {
        // Storage is unavailable.
      }
    }
    setRestoredKey(key);
  }, [key, restoreLatest]);

  useEffect(() => {
    if (restoredKey !== key) return;
    try {
      const serialized = serializeLatest(value);
      if (serialized === null) {
        window.localStorage.removeItem(key);
      } else {
        window.localStorage.setItem(key, serialized);
      }
    } catch {
      // Keep the value in memory.
    }
  }, [key, restoredKey, serializeLatest, value]);
}
