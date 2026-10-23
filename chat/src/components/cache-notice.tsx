"use client";

import { useEffect, useState } from "react";
import { Archive, LoaderCircle, X } from "lucide-react";
import { toast } from "sonner";
import {useChatActions, useChatSelector} from "./chat-provider";
import { getErrorMessage } from "@/lib/error-utils";
import { formatTokenCount } from "@/lib/session-status";
import { coldCache, formatAgo } from "@/lib/usage";
import { usePersisted } from "@/lib/use-persisted";

// Says, while the agent is idle, that its prompt cache expired with a large
// context (Claude Code reports how long it keeps it): the next message has
// the model read all of it again, at the full price and against the usage
// limits. Compacting first makes that message cheap. Dismissed, it stays
// away until the agent's next turn.
export function CacheNotice() {
  const agentUsage = useChatSelector((chat) => chat.agentUsage);
  const serverStatus = useChatSelector((chat) => chat.serverStatus);
  const pendingRequestCount = useChatSelector((chat) => chat.pendingRequestCount);
  const limitStop = useChatSelector((chat) => chat.limitStop);
  const loginRequired = useChatSelector((chat) => chat.loginRequired);
  const capabilities = useChatSelector((chat) => chat.capabilities);
  const agentType = useChatSelector((chat) => chat.agentType);
  const storageScope = useChatSelector((chat) => chat.storageScope);
  const {compact, sendMessage} = useChatActions();
  const [now, setNow] = useState(() => Date.now());
  // The expiry the notice was closed for, kept over a reload.
  const [dismissed, setDismissed] = useState("");
  usePersisted(`agentapi.chat.cold-cache-dismissed:${storageScope}`, dismissed, (value) => value || null, (stored) => setDismissed(stored ?? ""));
  const [compacting, setCompacting] = useState(false);
  const expiresAt = agentUsage?.prompt_cache?.expires_at;

  // Shows once the cache expires, and keeps its "ago" current after.
  useEffect(() => {
    if (!expiresAt) return;
    const tick = () => setNow(Date.now());
    let interval: ReturnType<typeof setInterval> | undefined;
    const wait = Math.max(0, Date.parse(expiresAt) - Date.now());
    const timeout = setTimeout(() => {
      tick();
      interval = setInterval(tick, 60_000);
    }, wait + 1000);
    tick();
    return () => {
      clearTimeout(timeout);
      clearInterval(interval);
    };
  }, [expiresAt]);

  // Claude Code compacts with the /compact message; other agents on request.
  const canCompact = Boolean(capabilities?.compact) || agentType === "claude";
  const cold = coldCache(agentUsage, now);
  const idle = serverStatus === "stable" && pendingRequestCount === 0 && !limitStop && !loginRequired;
  if (!cold || !idle || !canCompact || dismissed === expiresAt) return null;

  const runCompact = async () => {
    setCompacting(true);
    try {
      if (capabilities?.compact) {
        await compact();
      } else {
        await sendMessage("/compact");
      }
    } catch (error) {
      toast.error("Couldn't compact the context", { description: getErrorMessage(error) });
    } finally {
      setCompacting(false);
    }
  };

  return (
    <div
      data-cold-cache
      role="status"
      className="flex min-w-0 items-center gap-2 rounded-xl border bg-card px-3 py-2 text-xs text-muted-foreground shadow-sm"
    >
      <Archive className="size-3.5 shrink-0" aria-hidden="true" />
      <p className="min-w-0 flex-1">
        The prompt cache expired {formatAgo(cold.expiredAt, now)}: the next message reads all{" "}
        {formatTokenCount(cold.tokens)} tokens of context again. Compact first to make it cheaper.
      </p>
      <button
        type="button"
        onClick={() => void runCompact()}
        disabled={compacting}
        className="flex shrink-0 items-center gap-1 rounded-lg border px-2.5 py-1 font-medium text-foreground outline-none transition hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
      >
        {compacting && <LoaderCircle className="size-3 animate-spin" />}
        Compact
      </button>
      <button
        type="button"
        onClick={() => setDismissed(expiresAt ?? "")}
        className="shrink-0 rounded p-1 outline-none transition hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        title="Dismiss"
      >
        <X className="size-3.5" />
        <span className="sr-only">Dismiss</span>
      </button>
    </div>
  );
}
