"use client";

import {useEffect, useState} from "react";
import {useAgentAPIUrl} from "./chat-provider";

export function TerminalScreen() {
  const agentAPIUrl = useAgentAPIUrl();
  const [screen, setScreen] = useState("");

  useEffect(() => {
    const eventSource = new EventSource(`${agentAPIUrl}/internal/screen`);
    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as {screen?: string};
        if (typeof data.screen === "string") setScreen(data.screen);
      } catch {
        // Keep the last valid terminal snapshot.
      }
    };
    return () => eventSource.close();
  }, [agentAPIUrl]);

  return (
    <div className="min-h-0 flex-1 overflow-auto bg-zinc-950 p-3 text-zinc-100 sm:p-5">
      <pre className="min-h-full whitespace-pre font-mono text-xs leading-5 sm:text-[13px]">
        {screen || "Waiting for terminal output…"}
      </pre>
    </div>
  );
}
