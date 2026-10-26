"use client";

import {useState} from "react";
import {ArrowRight, Eye, EyeOff, TriangleAlert} from "lucide-react";
import {Button} from "@/components/ui/button";
import {ConstellationScene, HubMark} from "./brand";
import {cn} from "@/lib/utils";

// SignIn trades the hub token for the hub's session cookie.
export function SignIn({root, onSignedIn}: {root: string; onSignedIn: () => void}) {
  const [token, setToken] = useState("");
  const [shown, setShown] = useState(false);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState(false);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!token.trim()) {
      setError("Paste the hub's token first.");
      setAttempt((n) => n + 1);
      return;
    }
    setBusy(true);
    setError("");
    const response = await fetch(`${root}/api/session`, {
      method: "POST",
      credentials: "same-origin",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({token: token.trim()}),
    }).catch(() => undefined);
    setBusy(false);
    if (response?.ok) return onSignedIn();
    setError(response?.status === 401 ? "That token doesn't open this hub." : "The hub didn't answer. Is it still running?");
    setAttempt((n) => n + 1);
  };
  return (
    <main className="grid min-h-svh bg-background lg:grid-cols-[1.15fr_1fr]">
      <section className="hub-grid relative hidden flex-col justify-between overflow-hidden border-r p-12 lg:flex" aria-hidden="true">
        <div className="flex items-center gap-2.5">
          <HubMark className="size-8" />
          <span className="text-[15px] font-semibold tracking-tight">AgentAPI Hub</span>
        </div>
        <ConstellationScene className="mx-auto w-full max-w-[460px]" />
        <div className="max-w-md">
          <p className="text-4xl leading-[1.1] font-semibold tracking-tight text-balance">Every agent, one&nbsp;glance.</p>
          <p className="mt-4 text-[15px] leading-relaxed text-muted-foreground">
            The coding agents on all your machines, the ones that need you first, and any of them a tap away, on your desk or your phone.
          </p>
        </div>
      </section>
      <section className="flex items-center justify-center px-6 py-12">
        <form onSubmit={submit} className="hub-rise w-full max-w-sm">
          <div className="mb-10 flex items-center gap-2.5 lg:hidden">
            <HubMark className="size-8" />
            <span className="text-[15px] font-semibold tracking-tight">AgentAPI Hub</span>
          </div>
          <h1 className="text-2xl font-semibold tracking-tight">Sign in to your hub</h1>
          <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
            Use the token the hub runs with: <code className="rounded bg-muted px-1 py-0.5 font-mono text-[12px] text-foreground">--token</code> or{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[12px] text-foreground">AGENTAPI_HUB_TOKEN</code>.
          </p>
          <label htmlFor="hub-token" className="mt-8 block text-sm font-medium">
            Hub token
          </label>
          <div key={attempt} className={cn("relative mt-2", attempt > 0 && error && "hub-shake")}>
            <input
              id="hub-token"
              type={shown ? "text" : "password"}
              autoFocus
              autoComplete="current-password"
              spellCheck={false}
              value={token}
              onChange={(e) => setToken(e.target.value)}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? "hub-token-error" : undefined}
              className="h-11 w-full rounded-lg border bg-card pr-11 pl-3.5 font-mono text-sm shadow-xs outline-none transition-shadow placeholder:font-sans placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/40 aria-invalid:border-destructive"
              placeholder="Paste the token"
            />
            <button
              type="button"
              onClick={() => setShown((v) => !v)}
              className="absolute inset-y-0 right-0 flex w-11 items-center justify-center rounded-r-lg text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40"
              aria-label={shown ? "Hide the token" : "Show the token"}
            >
              {shown ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
            </button>
          </div>
          {error && (
            <p id="hub-token-error" role="alert" className="mt-2 flex items-center gap-1.5 text-sm text-destructive">
              <TriangleAlert className="size-4 shrink-0" /> {error}
            </p>
          )}
          <Button type="submit" size="lg" className="group mt-6 h-11 w-full text-[15px]" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
            <ArrowRight className="transition-transform group-hover:translate-x-0.5" />
          </Button>
          <p className="mt-6 text-xs leading-relaxed text-muted-foreground">
            The hub keeps you signed in for 30 days with a cookie made from the token, never the token itself.
          </p>
        </form>
      </section>
    </main>
  );
}
