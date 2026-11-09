"use client";

import {useEffect, useState} from "react";
import {KeyRound, RefreshCw, Server, Trash2, TriangleAlert} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {hubErrorText} from "@/lib/hub-api";
import {Note} from "../github-dialogs";

// ForgetMachineDialog confirms forgetting a machine that is offline: the
// hub drops it and the launcher token it pinned its name to, which a
// launcher set up again (a new token) needs before it can join.
export function ForgetMachineDialog({
  hostname,
  open,
  onClose,
  onForget,
}: {
  hostname: string;
  open: boolean;
  onClose: () => void;
  onForget: (hostname: string) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (open) setError("");
  }, [open]);
  const forget = async () => {
    setBusy(true);
    setError("");
    try {
      await onForget(hostname);
      toast.success(`Forgot ${hostname}`, {description: "Its launcher can join again, with any token."});
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
          <DialogTitle>
            Forget <span className="font-mono">{hostname}</span>?
          </DialogTitle>
          <DialogDescription>The hub drops the machine from its list, with the launcher token its name is pinned to.</DialogDescription>
        </DialogHeader>
        <ul className="divide-y rounded-xl border bg-card">
          <Note icon={<KeyRound />}>
            Its launcher, set up again or with a new token, can join under the same name. Until then the hub refuses any other token for it.
          </Note>
          <Note icon={<Server />}>Nothing changes on the machine: a launcher that reports again shows up again.</Note>
        </ul>
        {error && (
          <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" /> Couldn&apos;t forget it: {error}
          </p>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" className="h-10" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="button" variant="destructive" className="h-10" onClick={() => void forget()} disabled={busy}>
            {busy ? <RefreshCw className="animate-spin" /> : <Trash2 />} Forget
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
