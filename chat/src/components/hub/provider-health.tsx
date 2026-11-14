"use client";

import {HeartPulse, TriangleAlert} from "lucide-react";
import {providerFailing, providerHealthLine, providerHealthTitle, type ProviderHealthTotal} from "@/lib/provider-health";
import {useNow} from "@/lib/use-elapsed";
import {cn} from "@/lib/utils";

// ProviderHealthLine is how a provider answered the agents of the hub over
// the last hour, beside what they used it for: their calls, the share that
// failed, its median time to the first byte and the last error, in amber
// while it fails.
export function ProviderHealthLine({health}: {health: ProviderHealthTotal}) {
  const now = useNow(30_000);
  const failing = providerFailing(health);
  return (
    <p
      className={cn("flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground tabular-nums", failing && "text-amber-700 dark:text-amber-400")}
      title={`${providerHealthTitle(health, now)} Added up over the agents that report it.`}
      data-provider-health
      data-failing={failing || undefined}
    >
      {failing ? <TriangleAlert className="size-3 shrink-0" aria-hidden="true" /> : <HeartPulse className="size-3 shrink-0" aria-hidden="true" />}
      <span className="truncate">{providerHealthLine(health, now)}</span>
    </p>
  );
}
