"use client";

import {useEffect, useState} from "react";
import {Plus, RefreshCw, TriangleAlert} from "lucide-react";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";

const fieldClass =
  "h-11 w-full rounded-lg border bg-card px-3.5 text-sm shadow-xs outline-none transition-shadow placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/40";

// The longest group name the hub takes.
export const maxGroupName = 40;

// A group being named: a new one for an instance, or a rename.
export type Naming = {mode: "new"; id: string} | {mode: "rename"; from: string};

export function AddInstance({root, open, onOpenChange, onAdded}: {root: string; open: boolean; onOpenChange: (open: boolean) => void; onAdded: (id: string) => void}) {
  const [url, setURL] = useState("");
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const response = await fetch(`${root}/api/instances`, {
        method: "POST",
        credentials: "same-origin",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({url: url.trim(), token: token.trim()}),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) {
        setError(body.error ?? `The hub answered ${response.status}.`);
        return;
      }
      setURL("");
      setToken("");
      onOpenChange(false);
      onAdded(body.id);
    } catch {
      setError("The hub didn't answer.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>Add an instance</DialogTitle>
            <DialogDescription>
              For an AgentAPI that can&apos;t run <code className="font-mono text-[12px]">agentapi hub join</code>. Ones that join appear by themselves.
            </DialogDescription>
          </DialogHeader>
          <label className="block space-y-1.5">
            <span className="text-sm font-medium">URL</span>
            <input value={url} onChange={(e) => setURL(e.target.value)} placeholder="http://dev-1.vpn:3284" aria-label="URL" inputMode="url" className={fieldClass} />
          </label>
          <label className="block space-y-1.5">
            <span className="text-sm font-medium">
              API token <span className="font-normal text-muted-foreground">(if it needs one)</span>
            </span>
            <input type="password" value={token} onChange={(e) => setToken(e.target.value)} aria-label="API token" className={`${fieldClass} font-mono`} />
          </label>
          {error && (
            <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" /> {error}
            </p>
          )}
          <DialogFooter>
            <Button type="submit" className="h-10" disabled={!url.trim() || busy}>
              {busy ? <RefreshCw className="animate-spin" /> : <Plus />} {busy ? "Adding…" : "Add"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// GroupName names a new group for an instance, or renames a group.
export function GroupName({naming, onClose, onSave}: {naming: Naming | null; onClose: () => void; onSave: (name: string) => void}) {
  const [name, setName] = useState("");
  useEffect(() => {
    setName(naming?.mode === "rename" ? naming.from : "");
  }, [naming]);
  const trimmed = name.trim().replace(/\s+/g, " ");
  const rename = naming?.mode === "rename";
  return (
    <Dialog open={naming !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form
          onSubmit={(event) => {
            event.preventDefault();
            if (trimmed) onSave(trimmed);
          }}
          className="space-y-4"
        >
          <DialogHeader>
            <DialogTitle>{rename ? "Rename the group" : "New group"}</DialogTitle>
            <DialogDescription>
              {rename ? "Every instance in it moves to the new name." : "Groups are kept on the hub, so your phone and desktop show the same ones."}
            </DialogDescription>
          </DialogHeader>
          <label className="block space-y-1.5">
            <span className="flex items-baseline justify-between text-sm font-medium">
              Name
              <span className="text-xs font-normal text-muted-foreground tabular-nums">
                {name.length}/{maxGroupName}
              </span>
            </span>
            <input
              autoFocus
              value={name}
              maxLength={maxGroupName}
              onChange={(e) => setName(e.target.value)}
              placeholder="Client work"
              aria-label="Group name"
              className={fieldClass}
            />
          </label>
          <DialogFooter>
            <Button type="submit" className="h-10" disabled={!trimmed}>
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
