import {useEffect, useRef, useState} from "react";
import type {CostReport, createChatAPI} from "@/lib/chat-api";
import type {ServerStatus} from "@/lib/agent-status";

// How often a running task's cost is read again.
const RUNNING_REFRESH_MS = 15_000;

// What the open session spent, and its model calls (features.cost): read
// when the agent is ready, again whenever a turn ends, and now and then
// while one runs. null until read, or when the feature is off.
export function useSessionCost(
  api: ReturnType<typeof createChatAPI>,
  enabled: boolean,
  serverStatus: ServerStatus,
  ready: boolean,
): CostReport | null {
  const [report, setReport] = useState<CostReport | null>(null);
  const running = serverStatus === "running";
  const stable = serverStatus === "stable";

  // The last report, so a read asks for the calls made since only.
  const known = useRef<CostReport | null>(null);

  useEffect(() => {
    if (!enabled || !ready || !(running || stable)) return;
    let cancelled = false;
    const read = async () => {
      try {
        const next = await api.getCost(known.current);
        known.current = next;
        if (!cancelled) setReport(next);
      } catch {
        // The cost is a nicety: keep what was read before.
      }
    };
    void read();
    if (!running) return () => void (cancelled = true);
    const timer = window.setInterval(() => void read(), RUNNING_REFRESH_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [api, enabled, ready, running, stable]);

  useEffect(() => {
    if (enabled) return;
    known.current = null;
    setReport(null);
  }, [enabled]);

  return report;
}
