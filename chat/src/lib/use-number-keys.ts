"use client";

import { useEffect, useRef } from "react";
import { numberKeyIndex } from "./keys";

// Number keys pick an option of the decision card on screen (1 picks the
// first of count), unless the key is meant for something else; see
// numberKeyIndex.
export function useNumberKeys(count: number, onKey: (index: number) => void) {
  const handler = useRef(onKey);
  handler.current = onKey;
  useEffect(() => {
    if (count === 0) return;
    const onKeyDown = (event: KeyboardEvent) => {
      const index = numberKeyIndex(event, count);
      if (index === null) return;
      event.preventDefault();
      handler.current(index);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [count]);
}
