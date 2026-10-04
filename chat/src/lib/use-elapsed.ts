"use client";

import { useEffect, useState } from "react";

// "m:ss" since the agent started working, ticking every second; "" when it
// isn't working.
export function useWorkingElapsed(serverStatus: string): string {
  const [since, setSince] = useState<number | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    setSince((current) => (serverStatus === "running" ? current ?? Date.now() : null));
  }, [serverStatus]);
  useEffect(() => {
    if (since === null) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [since]);
  if (since === null) return "";
  const seconds = Math.max(0, Math.floor((now - since) / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
