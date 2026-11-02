"use client";

import {useCallback, useEffect, useRef, useState} from "react";
import {DEFAULT_LIST_WIDTH, MIN_LIST_WIDTH, clampListWidth, keyListWidth, maxListWidth, parseListWidth} from "@/lib/hub-list-width";
import {cn} from "@/lib/utils";

// Where the browser keeps the list's width.
const widthKey = "agentapi.hub.list-width";

function storedWidth(): number {
  try {
    return parseListWidth(localStorage.getItem(widthKey));
  } catch {
    return DEFAULT_LIST_WIDTH;
  }
}

export type ListWidth = {
  width: number;
  max: number;
  // resize sets the width, fitted to the window; keep stores it too.
  resize: (width: number, keep: boolean) => void;
};

// useListWidth is the width of the hub's list on a desktop: the one this
// browser keeps, fitted to the window as it resizes. A narrower window
// doesn't change the one kept, so the list widens again with the window.
export function useListWidth(): ListWidth {
  const [chosen, setChosen] = useState(storedWidth);
  const [windowWidth, setWindowWidth] = useState(() => (typeof window === "undefined" ? 0 : window.innerWidth));
  useEffect(() => {
    const onResize = () => setWindowWidth(window.innerWidth);
    onResize();
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  const resize = useCallback((width: number, keep: boolean) => {
    const fitted = clampListWidth(width, window.innerWidth);
    setChosen(fitted);
    if (!keep) return;
    try {
      localStorage.setItem(widthKey, String(fitted));
    } catch {}
  }, []);
  return {width: clampListWidth(chosen, windowWidth), max: maxListWidth(windowWidth), resize};
}

// ListEdge is the list's right edge on a desktop, over its border: drag it,
// or focus it and use the arrows (keyListWidth), to change the list's
// width; a double-click brings back the default. While it drags, a layer
// over the page keeps the pointer from the open chat's frame, the cursor
// from what it passes, and the drag from selecting text.
export function ListEdge({controls, width, max, resize}: ListWidth & {controls: string}) {
  const drag = useRef<{x: number; from: number; to: number} | null>(null);
  const [dragging, setDragging] = useState(false);
  const end = () => {
    const done = drag.current;
    if (!done) return;
    drag.current = null;
    setDragging(false);
    if (done.to !== done.from) resize(done.to, true);
  };
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize the agent list"
      aria-controls={controls}
      aria-valuenow={width}
      aria-valuemin={MIN_LIST_WIDTH}
      aria-valuemax={max}
      tabIndex={0}
      onPointerDown={(event) => {
        if (event.button !== 0) return;
        // Takes no focus (an open chat's composer keeps it) and starts no
        // selection.
        event.preventDefault();
        event.currentTarget.setPointerCapture(event.pointerId);
        drag.current = {x: event.clientX, from: width, to: width};
        setDragging(true);
      }}
      onPointerMove={(event) => {
        const at = drag.current;
        if (!at) return;
        at.to = at.from + event.clientX - at.x;
        resize(at.to, false);
      }}
      onPointerUp={end}
      onPointerCancel={end}
      onLostPointerCapture={end}
      onDoubleClick={() => resize(DEFAULT_LIST_WIDTH, true)}
      onKeyDown={(event) => {
        const next = keyListWidth(event, width, window.innerWidth);
        if (next === undefined) return;
        event.preventDefault();
        resize(next, true);
      }}
      className="group/edge absolute inset-y-0 -right-[5px] z-20 hidden w-[9px] cursor-col-resize touch-none outline-none select-none md:block"
    >
      <span
        aria-hidden="true"
        className={cn("absolute inset-y-0 left-[3px] w-[3px] transition-colors", dragging ? "bg-ring" : "group-hover/edge:bg-ring/60 group-focus-visible/edge:bg-ring")}
      />
      {dragging && <span aria-hidden="true" className="fixed inset-0" />}
    </div>
  );
}
