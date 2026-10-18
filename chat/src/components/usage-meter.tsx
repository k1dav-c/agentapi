"use client";

import { useEffect, useState } from "react";
import {useChat} from "./chat-provider";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuLabel, DropdownMenuTrigger } from "./ui/dropdown-menu";
import type { RateLimit } from "@/lib/chat-api";
import { contextPercent, formatResetIn, fullestLimit, usageTone, type UsageTone } from "@/lib/usage";
import { formatTokenCount as formatTokens } from "@/lib/session-status";
import { cn } from "@/lib/utils";

const TONE_STROKE: Record<UsageTone, string> = {
  ok: "stroke-foreground/70",
  warning: "stroke-status-warning",
  danger: "stroke-destructive",
};

const TONE_FILL: Record<UsageTone, string> = {
  ok: "bg-foreground/60",
  warning: "bg-status-warning",
  danger: "bg-destructive",
};

// A ring that fills with percent.
function Ring({ percent, tone }: { percent: number; tone: UsageTone }) {
  const radius = 6;
  const circumference = 2 * Math.PI * radius;
  return (
    <svg viewBox="0 0 16 16" className="size-4 -rotate-90" aria-hidden="true">
      <circle cx="8" cy="8" r={radius} fill="none" strokeWidth="2.5" className="stroke-muted-foreground/25" />
      <circle
        cx="8"
        cy="8"
        r={radius}
        fill="none"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeDasharray={circumference}
        strokeDashoffset={circumference * (1 - percent / 100)}
        className={TONE_STROKE[tone]}
      />
    </svg>
  );
}

function Bar({ percent, tone }: { percent: number; tone: UsageTone }) {
  return (
    <div className="h-1.5 overflow-hidden rounded-full bg-muted">
      <div className={cn("h-full rounded-full", TONE_FILL[tone])} style={{ width: `${Math.max(2, percent)}%` }} />
    </div>
  );
}

function LimitRow({ limit, now }: { limit: RateLimit; now: number }) {
  const percent = limit.used_percent;
  const tone = usageTone(percent, limit.status);
  const resetIn = formatResetIn(limit.resets_at, now);
  return (
    <div className="grid gap-1 px-2 py-1.5">
      <div className="flex items-baseline justify-between gap-3 text-xs">
        <span>{limit.label}</span>
        <span className={cn("tabular-nums", tone !== "ok" && "font-medium", tone === "danger" && "text-destructive")}>
          {percent !== undefined ? `${Math.round(percent)}%` : limit.status === "rejected" ? "reached" : ""}
        </span>
      </div>
      {percent !== undefined && <Bar percent={percent} tone={tone} />}
      {resetIn && (
        <span className="text-[11px] text-muted-foreground" title={limit.resets_at ? new Date(limit.resets_at).toLocaleString() : undefined}>
          Resets {resetIn}
        </span>
      )}
    </div>
  );
}

// How full the agent's context window is, on a ring in the composer's
// toolbar; its menu adds the account's rate limits (native agents that
// report them).
export function UsageMeter() {
  const { features, agentUsage } = useChat();
  // Reset times count down while the menu is open.
  const [now, setNow] = useState(() => Date.now());
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(timer);
  }, [open]);

  const context = agentUsage?.context;
  const limits = agentUsage?.rate_limits ?? [];
  if (!features.usage || (!context && limits.length === 0)) return null;

  const fullest = fullestLimit(limits);
  const percent = context ? contextPercent(context) : fullest?.used_percent ?? 0;
  const tone = context ? usageTone(percent) : usageTone(fullest?.used_percent, fullest?.status);
  const summary = context
    ? `Context ${Math.round(percent)}% full (${formatTokens(context.used)} of ${formatTokens(context.size)} tokens)`
    : `${fullest?.label}: ${Math.round(percent)}% used`;
  // The menu holds no items, which screen readers may skip: the button says
  // the limits too.
  const limitsSummary = limits
    .filter((limit) => limit.used_percent !== undefined)
    .map((limit) => `${limit.label} ${Math.round(limit.used_percent ?? 0)}% used`)
    .join(", ");

  return (
    <DropdownMenu onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-9 shrink-0 gap-1 rounded-full px-2 text-xs text-muted-foreground hover:text-foreground"
          title={summary}
          data-usage-meter
        >
          <Ring percent={percent} tone={tone} />
          <span className="tabular-nums" aria-hidden="true">{Math.round(percent)}%</span>
          <span className="sr-only">{limitsSummary ? `${summary}. ${limitsSummary}` : summary}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="max-h-[70dvh] w-72 overflow-y-auto">
        {context && (
          <>
            <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Context window</DropdownMenuLabel>
            <div className="grid gap-1 px-2 pb-2">
              <div className="flex items-baseline justify-between gap-3 text-xs">
                <span>
                  {formatTokens(context.used)} of {formatTokens(context.size)} tokens
                </span>
                <span className="tabular-nums">{Math.round(contextPercent(context))}%</span>
              </div>
              <Bar percent={contextPercent(context)} tone={usageTone(contextPercent(context))} />
              <span className="text-[11px] text-muted-foreground">After the last turn. The agent compacts it when it fills up.</span>
            </div>
          </>
        )}
        {limits.length > 0 && (
          <>
            <DropdownMenuLabel className="text-xs font-medium text-muted-foreground">Usage limits</DropdownMenuLabel>
            {limits.map((limit) => (
              <LimitRow key={limit.id} limit={limit} now={now} />
            ))}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
