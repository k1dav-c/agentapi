"use client";

import {useEffect, useRef} from "react";
import {useChatActions, useChatSelector} from "./chat-provider";
import {latestTaskEnd} from "@/lib/server-tasks";

// Tells the server the user saw the conversation (POST /tasks/seen) when a
// task ended while the page shows it, and when the page shows it again
// after one ended, so that every client of the AgentAPI (a dashboard that
// watches many, another tab) stops showing the task as unread.
export function SeenMarker() {
  const ended = useChatSelector((chat) => latestTaskEnd(chat.serverTasks));
  const {markSeen} = useChatActions();
  // The end of the task last marked seen.
  const marked = useRef<string | undefined>(undefined);

  useEffect(() => {
    const mark = () => {
      if (document.hidden || !ended || marked.current === ended) return;
      marked.current = ended;
      // An AgentAPI before 1.2 has no /tasks/seen; the next end tries again.
      void markSeen().catch(() => undefined);
    };
    mark();
    document.addEventListener("visibilitychange", mark);
    return () => document.removeEventListener("visibilitychange", mark);
  }, [ended, markSeen]);

  return null;
}
