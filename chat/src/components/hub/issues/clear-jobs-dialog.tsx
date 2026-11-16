"use client";

import {useEffect, useState} from "react";
import {MessageSquare, RefreshCw, RotateCcw, Trash2, TriangleAlert} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {hubErrorText} from "@/lib/hub-api";
import type {DispatchJob} from "@/lib/hub-dispatch";
import {Note} from "../github-dialogs";

// clearJobsTitle is the title of the dialog that clears n ended jobs.
export function clearJobsTitle(n: number): string {
  return `Clear ${n} ended ${n === 1 ? "job" : "jobs"}?`;
}

// ClearJobsDialog confirms clearing the ended jobs the Done section shows:
// the hub forgets them, their spend with them.
export function ClearJobsDialog({
  jobs,
  open,
  onClose,
  onClear,
}: {
  jobs: DispatchJob[];
  open: boolean;
  onClose: () => void;
  onClear: (keys: string[]) => Promise<number>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) setError("");
  }, [open]);
  const n = jobs.length;
  const clear = async () => {
    setBusy(true);
    setError("");
    try {
      const cleared = await onClear(jobs.map((job) => job.key));
      toast.success(cleared === 1 ? "Cleared 1 job" : `Cleared ${cleared} jobs`);
      onClose();
    } catch (e) {
      setError(hubErrorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(next) => !next && !busy && onClose()}>
      <DialogContent role="alertdialog" className="grid-cols-[minmax(0,1fr)] sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{clearJobsTitle(n)}</DialogTitle>
          <DialogDescription>The hub forgets them, with their runs and spend. Nothing changes on GitHub or on the machines.</DialogDescription>
        </DialogHeader>
        <ul className="max-h-40 overflow-y-auto rounded-xl border bg-card px-3 py-2 text-sm">
          {jobs.map((job) => (
            <li key={job.key} className="flex min-w-0 items-baseline gap-2 py-0.5">
              <span className="shrink-0 font-mono text-xs text-muted-foreground">
                {job.repo}#{job.issue}
              </span>
              <span className="truncate">{job.title}</span>
            </li>
          ))}
        </ul>
        <ul className="divide-y rounded-xl border bg-card">
          <Note icon={<MessageSquare />}>The cost comments stay on their pull requests and issues.</Note>
          <Note icon={<RotateCcw />}>An issue still open shows under To do again, and starts a new job when assigned.</Note>
        </ul>
        {error && (
          <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" /> Couldn&apos;t clear them: {error}
          </p>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" className="h-10" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="button" variant="destructive" className="h-10" onClick={() => void clear()} disabled={busy || n === 0}>
            {busy ? <RefreshCw className="animate-spin" /> : <Trash2 />} Clear
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
