"use client";

import React, {useCallback, useEffect, useLayoutEffect, useRef, useState} from "react";
import {Maximize2} from "lucide-react";
import {Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger} from "./ui/dialog";

const tableClass = "border-collapse text-left text-sm";

// A Markdown table. In a narrow column a wide one scrolls sideways: a shade
// at the right edge says there is more, and a button opens the table in a
// popup, as wide as the window lets it be. The popup's table is the same
// rows, not another MarkdownTable, so it has no button of its own.
export function MarkdownTable({children}: {children: React.ReactNode}) {
  const scroller = useRef<HTMLDivElement>(null);
  const [more, setMore] = useState(false);
  const measure = useCallback(() => {
    const el = scroller.current;
    if (el) setMore(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
  }, []);
  // After every render (a streaming reply adds rows), and when the column
  // or the table is resized.
  useLayoutEffect(measure);
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    if (el.firstElementChild) observer.observe(el.firstElementChild);
    return () => observer.disconnect();
  }, [measure]);

  return (
    <div className="group/table relative my-3">
      <div ref={scroller} onScroll={measure} className="max-w-full overflow-x-auto rounded-lg border">
        {/* The last header cell leaves room for the button. */}
        <table className={`w-full ${tableClass} [&_th:last-child]:pr-10`}>{children}</table>
      </div>
      {more && (
        <div
          data-slot="table-more"
          aria-hidden
          className="pointer-events-none absolute inset-y-px right-px w-6 rounded-r-lg bg-gradient-to-l from-foreground/10 to-transparent"
        />
      )}
      <Dialog>
        <DialogTrigger asChild>
          <button
            type="button"
            className="absolute right-2 top-2 grid size-7 place-items-center rounded-md border bg-background text-muted-foreground opacity-0 shadow-sm transition hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-hover/table:opacity-100 [@media(hover:none)]:opacity-100"
            aria-label="Open the table"
            title="Open the table"
          >
            <Maximize2 className="size-3.5" />
          </button>
        </DialogTrigger>
        {/* Centered as the image preview is (inset-x-0, mx-auto), so it can
            be as wide as its table. The header row stays in view as the
            rows scroll; a sticky cell needs a background that hides them. */}
        <DialogContent className="inset-x-0 mx-auto flex max-h-[90dvh] w-fit max-w-[min(80rem,calc(100vw-2rem))] translate-x-0 flex-col gap-2 p-3 data-[state=closed]:slide-out-to-left-0 data-[state=open]:slide-in-from-left-0 sm:max-w-[min(80rem,calc(100vw-2rem))]">
          <DialogTitle className="pr-8 text-sm font-medium">Table</DialogTitle>
          <DialogDescription className="sr-only">The table from the message, at full width</DialogDescription>
          <div className="min-h-0 overflow-auto rounded-lg border">
            {/* On a phone the popup is no wider than the column: headers keep
                to one line and cells to a few characters, so short CJK words
                don't break letter by letter; the popup scrolls instead. */}
            <table className={`${tableClass} [&_td]:min-w-20 [&_th]:sticky [&_th]:top-0 [&_th]:z-10 [&_th]:whitespace-nowrap [&_th]:bg-muted`}>{children}</table>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
