"use client";

import {ArrowDown, ArrowUp} from "lucide-react";
import {Button} from "../ui/button";
import type {useTranscriptScroll} from "./use-transcript-scroll";

// The transcript's floating buttons: to the previous and next task, and
// back to the latest message (with how many updates came meanwhile).
export function JumpButtons({scroll}: {scroll: ReturnType<typeof useTranscriptScroll>}) {
  return (
    <>
      {scroll.showTaskButtons && (
        <div className="absolute bottom-4 right-3 z-10 flex overflow-hidden rounded-full border bg-background/95 shadow-lg backdrop-blur sm:right-4">
          <Button
            type="button"
            size="icon"
            variant="ghost"
            disabled={!scroll.canScrollToPreviousUser}
            onClick={scroll.scrollToPreviousUserMessage}
            className="size-11 rounded-none"
            title="Previous task"
          >
            <ArrowUp />
            <span className="sr-only">Jump to previous task</span>
          </Button>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            disabled={!scroll.canScrollToNextUser}
            onClick={scroll.scrollToNextUserMessage}
            className="size-11 rounded-none border-l"
            title="Next task"
          >
            <ArrowDown />
            <span className="sr-only">Jump to next task</span>
          </Button>
        </div>
      )}

      {scroll.showScrollButton && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => scroll.scrollToBottom()}
          className="absolute bottom-4 left-3 z-10 h-11 gap-2 rounded-full bg-background/95 px-3 shadow-lg backdrop-blur sm:left-1/2 sm:-translate-x-1/2"
          title="Jump to latest message"
        >
          <ArrowDown className="size-3.5" />
          <span>
            {scroll.unreadCount > 0
              ? `${scroll.unreadCount} new ${scroll.unreadCount === 1 ? "update" : "updates"}`
              : "Jump to latest"}
          </span>
        </Button>
      )}
    </>
  );
}
