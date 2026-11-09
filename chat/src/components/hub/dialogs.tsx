"use client";

import {useEffect, useState} from "react";
import {Plus, RefreshCw, Trash2, TriangleAlert} from "lucide-react";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {displayName, type HubInstance, type HubWire} from "@/lib/hub";
import {hubLimits} from "@/lib/hub-schema";
import {JoinMachine} from "./join-machine";
import {fieldClass, sectionClass} from "./styles";


// The longest group name the hub takes.
export const maxGroupName = hubLimits.max_group_name;

// A group being named: a new one for an instance, or a rename.
export type Naming = {mode: "new"; id: string} | {mode: "rename"; from: string};

// AddInstance is the Add dialog: how to join a machine to the hub, and an
// AgentAPI added by its URL.
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
        body: JSON.stringify({url: url.trim(), token: token.trim()} satisfies HubWire["AddInstanceBody"]),
      });
      // The instance added, or why not.
      const body = (await response.json().catch(() => ({}))) as Partial<HubWire["Instance"] & HubWire["ErrorBody"]>;
      if (!response.ok) {
        setError(body.error ?? `The hub answered ${response.status}.`);
        return;
      }
      setURL("");
      setToken("");
      onOpenChange(false);
      onAdded((body as HubWire["Instance"]).id);
    } catch {
      setError("The hub didn't answer.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add agents</DialogTitle>
          <DialogDescription>Join a machine, and its AgentAPIs show up here by themselves, now and after restarts.</DialogDescription>
        </DialogHeader>
        <section aria-labelledby="hub-join-machine" className="space-y-3">
          <h3 id="hub-join-machine" className={sectionClass}>
            Join a machine
          </h3>
          <JoinMachine root={root} />
        </section>
        <form onSubmit={submit} className="space-y-3 border-t pt-5">
          <div className="space-y-1">
            <h3 className={sectionClass}>Add one by its URL</h3>
            <p className="text-sm text-muted-foreground">
              For an AgentAPI that can&apos;t run <code className="font-mono text-[12px]">agentapi hub join</code>.
            </p>
          </div>
          <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_11rem_auto] sm:items-end">
            <label className="block space-y-1.5">
              <span className="text-sm font-medium">URL</span>
              <input value={url} onChange={(e) => setURL(e.target.value)} placeholder="http://dev-1.vpn:3284" aria-label="URL" inputMode="url" className={fieldClass} />
            </label>
            <label className="block space-y-1.5">
              <span className="text-sm font-medium">API token</span>
              <input
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="If it needs one"
                aria-label="API token"
                className={`${fieldClass} font-mono placeholder:font-sans`}
              />
            </label>
            <Button type="submit" className="h-11" disabled={!url.trim() || busy}>
              {busy ? <RefreshCw className="animate-spin" /> : <Plus />} {busy ? "Adding…" : "Add"}
            </Button>
          </div>
          {error && (
            <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" /> {error}
            </p>
          )}
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

// forgetStoppedTitle is the title of the dialog that forgets n stopped
// agents.
export function forgetStoppedTitle(n: number): string {
  return `Forget ${n} stopped ${n === 1 ? "agent" : "agents"}?`;
}

// StoppedAgents lists the agents Forget all stopped forgets: each one's
// name and machine, scrolling when they are many.
export function StoppedAgents({instances}: {instances: readonly HubInstance[]}) {
  return (
    <ul aria-label="Stopped agents" className="max-h-60 divide-y overflow-y-auto rounded-lg border bg-card text-sm">
      {instances.map((instance) => (
        <li key={instance.id} className="flex items-baseline justify-between gap-3 px-3 py-2">
          <span className="min-w-0 truncate font-medium">{displayName(instance)}</span>
          {instance.hostname && <span className="max-w-[45%] shrink-0 truncate text-xs text-muted-foreground">{instance.hostname}</span>}
        </li>
      ))}
    </ul>
  );
}

// ForgetStopped asks before it forgets the stopped agents (instances);
// null is closed. The list stays as it was asked: one that stops meanwhile
// isn't added under the reader's hand.
export function ForgetStopped({
  instances,
  onClose,
  onForget,
}: {
  instances: readonly HubInstance[] | null;
  onClose: () => void;
  onForget: (ids: string[]) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const n = instances?.length ?? 0;
  const forget = async () => {
    if (!instances) return;
    setBusy(true);
    try {
      await onForget(instances.map((instance) => instance.id));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={instances !== null} onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{forgetStoppedTitle(n)}</DialogTitle>
          <DialogDescription>The hub stops listing {n === 1 ? "it" : "them"}. One that reports to the hub again comes back by itself.</DialogDescription>
        </DialogHeader>
        {instances && <StoppedAgents instances={instances} />}
        <DialogFooter>
          <Button variant="outline" className="h-10" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="destructive" className="h-10" onClick={() => void forget()} disabled={busy || n === 0}>
            {busy ? <RefreshCw className="animate-spin" /> : <Trash2 />} Forget
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
