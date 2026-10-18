"use client";

import { useEffect, useState } from "react";
import { formatDuration, secondsBetween } from "./clock";

// How long the agent has been working (formatDuration), ticking every
// second; "" when it isn't working.
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
  return formatDuration(secondsBetween(since, now));
}

// The time now, every interval milliseconds while interval isn't null.
export function useNow(interval: number | null): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (interval === null) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(timer);
  }, [interval]);
  return now;
}
