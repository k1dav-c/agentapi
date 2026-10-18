"use client";

import {memo, useMemo} from "react";
import {classifyDiffLines, type DiffLineKind} from "@/lib/diff";
import {cn} from "@/lib/utils";

const LINE_CLASS: Record<DiffLineKind, string> = {
  header: "font-semibold",
  hunk: "bg-white/[0.04] text-term-dim",
  add: "bg-diff-add/12 text-diff-add",
  del: "bg-diff-del/12 text-diff-del",
  context: "",
  meta: "italic text-term-dim",
};

// A unified diff in the terminal's colors: added and removed lines colored,
// hunk headers dimmed. Long lines scroll sideways rather than wrap, so the
// columns stay aligned; the box is focusable so the keyboard can scroll it.
// Memoized: the chat re-renders its parents while the agent streams.
export const DiffView = memo(function DiffView({diff, label, className}: {diff: string; label: string; className?: string}) {
  const lines = useMemo(() => classifyDiffLines(diff), [diff]);
  return (
    <div
      role="region"
      aria-label={label}
      tabIndex={0}
      className={cn(
        "overflow-auto overscroll-x-contain rounded-md bg-term font-mono text-xs leading-5 text-term-foreground outline-none [tab-size:4] focus-visible:ring-2 focus-visible:ring-ring",
        className,
      )}
    >
      <div className="min-w-max py-1.5">
        {lines.map((line, index) => (
          <div key={index} data-diff-line={line.kind} className={cn("whitespace-pre px-3", LINE_CLASS[line.kind])}>
            {line.text || " "}
          </div>
        ))}
      </div>
    </div>
  );
});
