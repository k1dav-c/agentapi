"use client";

import {TriangleAlert} from "lucide-react";
import {Button} from "@/components/ui/button";
import {cn} from "@/lib/utils";

// ReadError says the hub couldn't read something (what), why, and has Try
// again; disabled while it reads again.
export function ReadError({what, error, onRetry, disabled = false, className}: {what: React.ReactNode; error: string; onRetry: () => void; disabled?: boolean; className?: string}) {
  return (
    <div role="alert" className={cn("flex flex-wrap items-center gap-3 rounded-xl border border-state-fault/25 bg-state-fault/[0.06] px-3.5 py-3 text-sm", className)}>
      <TriangleAlert className="size-4 shrink-0 text-state-fault" aria-hidden="true" />
      <p className="min-w-0 flex-1">
        {what} <span className="text-muted-foreground">· {error}</span>
      </p>
      <Button type="button" size="sm" variant="outline" disabled={disabled} onClick={onRetry}>
        Try again
      </Button>
    </div>
  );
}
