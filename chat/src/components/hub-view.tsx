"use client";

import {useCallback, useEffect, useMemo, useState} from "react";
import {ArrowLeft, Plus, RefreshCw, Trash2} from "lucide-react";
import {Button} from "@/components/ui/button";
import {Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import {agentDisplayName, parseAgentType} from "@/lib/agents";
import {
  applyHubEvent,
  chatURL,
  displayName,
  hubRoot,
  instanceState,
  needsYou,
  sortInstances,
  stateLabel,
  usagePercent,
  type HubEvent,
  type HubInstance,
  type InstanceState,
} from "@/lib/hub";
import {cn} from "@/lib/utils";

const basePath = (process.env.NEXT_PUBLIC_BASE_PATH ?? "/chat").replace(/\/$/, "");

const dotClass: Record<InstanceState, string> = {
  "needs-you": "bg-amber-500",
  working: "bg-blue-500 animate-pulse",
  unread: "bg-green-500",
  idle: "bg-muted-foreground/40",
  connecting: "bg-muted-foreground/40 animate-pulse",
  unreachable: "bg-red-500",
  stopped: "border border-muted-foreground/60 bg-transparent",
};

// HubView is the hub's page: sign in, the instances the hub watches in a
// sidebar (what needs you first), and the selected one in the chat. On a
// narrow screen the list and the chat take turns.
export function HubView() {
  const root = useMemo(() => (typeof window === "undefined" ? "" : hubRoot(basePath, window.location.origin)), []);
  const [signedIn, setSignedIn] = useState<boolean | undefined>(undefined);
  const [instances, setInstances] = useState<Map<string, HubInstance>>(new Map());
  const [connected, setConnected] = useState(false);
  const [selected, setSelected] = useState<string | undefined>(() =>
    typeof window === "undefined" ? undefined : decodeURIComponent(window.location.hash.slice(1)) || undefined,
  );
  const [adding, setAdding] = useState(false);

  useEffect(() => {
    if (!root) return;
    fetch(`${root}/api/session`, {credentials: "same-origin"})
      .then((r) => r.json())
      .then((body: {authenticated?: boolean}) => setSignedIn(Boolean(body.authenticated)))
      .catch(() => setSignedIn(false));
  }, [root]);

  // The instances and their changes, from one stream.
  useEffect(() => {
    if (!signedIn || !root) return;
    const source = new EventSource(`${root}/api/events`, {withCredentials: true});
    const on = (type: HubEvent["type"]) => (message: MessageEvent<string>) => {
      const data = JSON.parse(message.data);
      const event: HubEvent =
        type === "instances" ? {type, instances: data.instances ?? []} : type === "instance" ? {type, instance: data} : {type, id: data.id};
      setInstances((previous) => applyHubEvent(previous, event));
    };
    source.addEventListener("instances", on("instances") as EventListener);
    source.addEventListener("instance", on("instance") as EventListener);
    source.addEventListener("removed", on("removed") as EventListener);
    source.onopen = () => setConnected(true);
    source.onerror = () => {
      setConnected(false);
      // A session that expired comes back as a sign-in.
      fetch(`${root}/api/session`, {credentials: "same-origin"})
        .then((r) => r.json())
        .then((body: {authenticated?: boolean}) => {
          if (!body.authenticated) setSignedIn(false);
        })
        .catch(() => undefined);
    };
    return () => source.close();
  }, [signedIn, root]);

  const rows = useMemo(() => sortInstances(instances.values()), [instances]);
  const waiting = needsYou(instances.values());
  useEffect(() => {
    document.title = waiting > 0 ? `(${waiting}) AgentAPI hub` : "AgentAPI hub";
  }, [waiting]);

  const select = useCallback((id: string | undefined) => {
    setSelected(id);
    history.replaceState(null, "", id ? `#${encodeURIComponent(id)}` : window.location.pathname);
  }, []);

  const forget = async (id: string) => {
    await fetch(`${root}/api/instances/${encodeURIComponent(id)}`, {method: "DELETE", credentials: "same-origin"});
    if (selected === id) select(undefined);
  };

  if (signedIn === undefined) return <div className="p-4 text-center text-sm text-muted-foreground">Loading the hub...</div>;
  if (!signedIn) return <SignIn root={root} onSignedIn={() => setSignedIn(true)} />;

  const current = selected ? instances.get(selected) : undefined;
  return (
    <main className="flex h-svh overflow-hidden bg-background">
      <aside
        className={cn(
          "flex w-full flex-col border-r md:w-80 md:shrink-0",
          current ? "hidden md:flex" : "flex",
        )}
      >
        <header className="flex items-center justify-between gap-2 border-b px-3 py-2">
          <div className="min-w-0">
            <h1 className="text-sm font-semibold">AgentAPI hub</h1>
            <p className="text-xs text-muted-foreground">
              {connected ? `${rows.length} instance${rows.length === 1 ? "" : "s"}` : "Reconnecting..."}
              {waiting > 0 && ` · ${waiting} need${waiting === 1 ? "s" : ""} you`}
            </p>
          </div>
          <Button variant="ghost" size="icon" aria-label="Add an instance" onClick={() => setAdding(true)}>
            <Plus />
          </Button>
        </header>
        <ul className="flex-1 overflow-y-auto" aria-label="Instances">
          {rows.length === 0 && (
            <li className="p-4 text-sm text-muted-foreground">
              No instance yet. On a machine that runs AgentAPI, run <code className="font-mono">agentapi hub join {root}/hooks --secret …</code>, or add one by its URL.
            </li>
          )}
          {rows.map((instance) => (
            <InstanceRow
              key={instance.id}
              instance={instance}
              selected={instance.id === selected}
              onSelect={() => select(instance.id)}
              onForget={() => void forget(instance.id)}
            />
          ))}
        </ul>
      </aside>
      <section className={cn("min-w-0 flex-1 flex-col", current ? "flex" : "hidden md:flex")}>
        {current ? (
          <>
            <div className="flex items-center gap-2 border-b px-2 py-1 md:hidden">
              <Button variant="ghost" size="sm" onClick={() => select(undefined)}>
                <ArrowLeft /> Instances
              </Button>
              <span className="truncate text-sm font-medium">{displayName(current)}</span>
            </div>
            <iframe
              key={current.id}
              title={displayName(current)}
              src={chatURL(basePath, root, current.id)}
              className="min-h-0 w-full flex-1 border-0"
            />
          </>
        ) : (
          <div className="flex flex-1 items-center justify-center p-8 text-sm text-muted-foreground">Pick an instance to open its chat.</div>
        )}
      </section>
      <AddInstance root={root} open={adding} onOpenChange={setAdding} onAdded={(id) => select(id)} />
    </main>
  );
}

function InstanceRow({instance, selected, onSelect, onForget}: {instance: HubInstance; selected: boolean; onSelect: () => void; onForget: () => void}) {
  const state = instanceState(instance);
  const summary = instance.summary;
  const usage = usagePercent(summary);
  const agent = instance.agent_type ? agentDisplayName(parseAgentType(instance.agent_type), instance.agent_type) : "";
  return (
    <li className={cn("group border-b", selected && "bg-accent")}>
      <button type="button" onClick={onSelect} className="flex w-full items-start gap-3 px-3 py-2.5 text-left hover:bg-accent/60">
        <span className={cn("mt-1.5 size-2.5 shrink-0 rounded-full", dotClass[state])} aria-hidden />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline justify-between gap-2">
            <span className="truncate text-sm font-medium">{displayName(instance)}</span>
            <span className={cn("shrink-0 text-xs", state === "needs-you" ? "font-medium text-amber-600 dark:text-amber-400" : "text-muted-foreground")}>
              {stateLabel(instance)}
            </span>
          </span>
          <span className="block truncate text-xs text-muted-foreground">
            {summary?.title || instance.error || instance.url}
          </span>
          <span className="flex min-w-0 gap-2 text-[11px] text-muted-foreground/80">
            {agent && <span className="shrink-0">{agent}</span>}
            {instance.hostname && <span className="min-w-0 truncate">{instance.hostname}</span>}
            {summary && summary.queued_messages > 0 && <span className="shrink-0 whitespace-nowrap">{summary.queued_messages} queued</span>}
            {usage !== undefined && <span className="shrink-0 whitespace-nowrap">{Math.round(usage)}% limit</span>}
          </span>
        </span>
      </button>
      {(state === "stopped" || state === "unreachable") && (
        <div className="hidden justify-end px-3 pb-2 group-hover:flex">
          <Button variant="ghost" size="sm" onClick={onForget}>
            <Trash2 /> Forget
          </Button>
        </div>
      )}
    </li>
  );
}

function SignIn({root, onSignedIn}: {root: string; onSignedIn: () => void}) {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setError("");
    const response = await fetch(`${root}/api/session`, {
      method: "POST",
      credentials: "same-origin",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({token}),
    }).catch(() => undefined);
    if (response?.ok) onSignedIn();
    else setError(response?.status === 401 ? "Wrong token." : "The hub didn't answer.");
  };
  return (
    <main className="flex h-svh items-center justify-center bg-background p-4">
      <form onSubmit={submit} className="w-full max-w-sm space-y-3 rounded-lg border p-5">
        <h1 className="text-base font-semibold">AgentAPI hub</h1>
        <p className="text-sm text-muted-foreground">Sign in with the hub&apos;s token (agentapi hub serve --token).</p>
        <input
          type="password"
          autoFocus
          autoComplete="current-password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm"
          placeholder="Hub token"
          aria-label="Hub token"
        />
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button type="submit" className="w-full" disabled={!token}>
          Sign in
        </Button>
      </form>
    </main>
  );
}

function AddInstance({root, open, onOpenChange, onAdded}: {root: string; open: boolean; onOpenChange: (open: boolean) => void; onAdded: (id: string) => void}) {
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
        body: JSON.stringify({url, token}),
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
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} className="space-y-3">
          <DialogHeader>
            <DialogTitle>Add an instance</DialogTitle>
            <DialogDescription>An AgentAPI the hub can reach, such as http://dev-1.vpn:3284. Instances that run agentapi hub join appear by themselves.</DialogDescription>
          </DialogHeader>
          <input value={url} onChange={(e) => setURL(e.target.value)} placeholder="http://dev-1.vpn:3284" aria-label="URL" className="w-full rounded-md border bg-background px-3 py-2 text-sm" />
          <input type="password" value={token} onChange={(e) => setToken(e.target.value)} placeholder="API token (if it needs one)" aria-label="API token" className="w-full rounded-md border bg-background px-3 py-2 text-sm" />
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="submit" disabled={!url || busy}>
              {busy ? <RefreshCw className="animate-spin" /> : <Plus />} Add
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
