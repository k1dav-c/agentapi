"use client";

import type {KeyboardEvent} from "react";
import {Tooltip, TooltipContent, TooltipTrigger} from "./ui/tooltip";
import {useFileViewer} from "./file-viewer-context";
import type {FilePathRef} from "@/lib/file-refs";
import {cn} from "@/lib/utils";

// A path on one line that keeps the file name in view: when it doesn't fit,
// the directory gives way first, cut at its start (…/src/lib/), then the
// name.
export function PathLabel({path, className}: {path: string; className?: string}) {
  const slash = path.lastIndexOf("/");
  const directory = path.slice(0, slash + 1);
  const name = path.slice(slash + 1);
  return (
    <span className={cn("flex min-w-0 font-mono", className)}>
      {directory && (
        // Right to left, so the ellipsis cuts the start; the bdi keeps the
        // path itself in order.
        <span className="min-w-0 truncate text-muted-foreground [direction:rtl]">
          <bdi>{directory}</bdi>
        </span>
      )}
      <span className="max-w-full shrink-0 truncate">{name}</span>
    </span>
  );
}

// Inline code in a message, as the Markdown renderer draws it.
export const inlineCodeClass = "rounded-[5px] border border-border bg-muted px-1 py-px font-mono text-[0.86em] text-foreground";

// Inline code in an agent's message that names a file: opens the file
// viewer at its line. Inline (not a <button>) so a long path wraps with the
// text around it.
export function FilePathCode({text, fileRef}: {text: string; fileRef: FilePathRef}) {
  const viewer = useFileViewer();
  if (!viewer) return <code className={inlineCodeClass}>{text}</code>;
  const open = () => viewer.open({path: fileRef.path, line: fileRef.line, tab: "file"});
  const label = fileRef.line ? `Open file at line ${fileRef.line}` : "Open file";
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <code
          role="button"
          tabIndex={0}
          aria-haspopup="dialog"
          aria-label={`${label}: ${text}`}
          onClick={open}
          onKeyDown={(event: KeyboardEvent) => {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              open();
            }
          }}
          className={cn(
            inlineCodeClass,
            // Dotted like the file names on tool calls, so it reads as a link
            // without hovering (touch screens can't).
            "cursor-pointer underline decoration-muted-foreground/50 decoration-dotted decoration-1 underline-offset-[3px] outline-none transition-colors [overflow-wrap:anywhere] hover:border-ring hover:decoration-current hover:decoration-solid focus-visible:ring-2 focus-visible:ring-ring",
          )}
        >
          {text}
        </code>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}
