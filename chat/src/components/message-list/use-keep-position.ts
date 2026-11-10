"use client";

import {useCallback, useEffect, useRef} from "react";

// How long the transcript keeps the reader's place after tasks were put
// above it: the tasks render progressively, each growing in turn.
const HOLD_MS = 3000;

// useKeepPosition keeps what the reader sees in place while earlier tasks
// are put above it (Show earlier tasks, scrolling to the top): hold notes
// where the first task shown is, and until the reader scrolls (or
// HOLD_MS passed) the transcript scrolls by as much as it moved. Browsers
// that anchor scrolling themselves move it back already, and then nothing
// changes. Call restore once the tasks rendered.
export function useKeepPosition(scrollArea: HTMLElement | null, layout: string) {
  const anchor = useRef<{element: Element; top: number; until: number} | null>(null);

  const hold = useCallback(() => {
    const element = scrollArea?.querySelector(".task-entry");
    if (!element) return;
    anchor.current = {element, top: element.getBoundingClientRect().top, until: Date.now() + HOLD_MS};
  }, [scrollArea]);

  const restore = useCallback(() => {
    const held = anchor.current;
    if (!scrollArea || !held) return;
    if (Date.now() > held.until || !held.element.isConnected) {
      anchor.current = null;
      return;
    }
    const moved = held.element.getBoundingClientRect().top - held.top;
    if (Math.abs(moved) >= 1) scrollArea.scrollTop += moved;
  }, [scrollArea]);

  useEffect(() => {
    if (!scrollArea) return;
    const observer = new ResizeObserver(restore);
    for (const child of Array.from(scrollArea.children)) observer.observe(child);
    // The reader scrolls on: their place is theirs again.
    const release = () => {
      anchor.current = null;
    };
    scrollArea.addEventListener("wheel", release, {passive: true});
    scrollArea.addEventListener("touchstart", release, {passive: true});
    scrollArea.addEventListener("keydown", release);
    return () => {
      observer.disconnect();
      scrollArea.removeEventListener("wheel", release);
      scrollArea.removeEventListener("touchstart", release);
      scrollArea.removeEventListener("keydown", release);
    };
  }, [scrollArea, layout, restore]);

  return {hold, restore};
}
