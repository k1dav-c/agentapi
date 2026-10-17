"use client";

import {useEffect, useRef, useState, type FormEvent} from "react";
import {KeyRound, TriangleAlert} from "lucide-react";
import {Button} from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {exchangeToken, isCrossOrigin, serverHost, setSignInHandler} from "@/lib/auth";

interface Prompt {
  baseURL: string;
  resolve: (signedIn: boolean) => void;
}

const ERRORS = {
  rejected: "The server did not accept this token.",
  "cookie-blocked":
    "The server accepted the token, but the browser didn't keep the session cookie, so the chat can't stay signed in. Allow cookies for this site and try again.",
} as const;

// Asks for the API token when the server answers 401, and trades it for a
// session cookie. Requests waiting on the sign-in retry once it succeeds.
export function AuthDialog() {
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const promptRef = useRef<Prompt | null>(null);

  useEffect(() => {
    setSignInHandler((baseURL) =>
      new Promise<boolean>((resolve) => {
        const next = {baseURL, resolve};
        promptRef.current = next;
        setPrompt(next);
        setToken("");
        setError("");
      }),
    );
    return () => {
      setSignInHandler(null);
      promptRef.current?.resolve(false);
      promptRef.current = null;
    };
  }, []);

  const finish = (signedIn: boolean) => {
    promptRef.current?.resolve(signedIn);
    promptRef.current = null;
    setPrompt(null);
    setToken("");
    setError("");
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!prompt || !token.trim() || submitting) return;
    setSubmitting(true);
    setError("");
    try {
      const result = await exchangeToken(prompt.baseURL, token.trim());
      if (result === "signed-in" || result === "not-required") {
        finish(true);
      } else {
        setError(ERRORS[result]);
      }
    } catch {
      setError("Could not reach the server.");
    } finally {
      setSubmitting(false);
    }
  };

  // The token goes to the server the chat talks to, which a link with
  // ?url= can point anywhere: say which server asks, and warn when it isn't
  // the one that serves this page.
  const host = prompt ? serverHost(prompt.baseURL) : "";
  const elsewhere = prompt ? isCrossOrigin(prompt.baseURL) : false;
  const describedBy = [elsewhere && "auth-dialog-elsewhere", error && "auth-dialog-error"].filter(Boolean).join(" ");

  return (
    <Dialog open={prompt !== null} onOpenChange={(open) => { if (!open) finish(false); }}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <KeyRound className="size-4" />
              Sign in to AgentAPI
            </DialogTitle>
            <DialogDescription>
              The AgentAPI server at <span className="font-mono text-xs font-medium text-foreground" data-auth-host>{host}</span> was
              started with <code className="font-mono text-xs">--api-token</code>.
              Enter the token to continue. It is exchanged for a session cookie and not stored by the page.
            </DialogDescription>
          </DialogHeader>
          {elsewhere && (
            <p
              id="auth-dialog-elsewhere"
              className="flex gap-2 rounded-md border border-status-warning/40 bg-status-warning/10 px-3 py-2 text-xs text-status-warning"
              data-auth-elsewhere
            >
              <TriangleAlert className="mt-px size-3.5 shrink-0" />
              <span>
                This page comes from <span className="font-mono">{globalThis.location?.host}</span>, but the token goes to{" "}
                <span className="font-mono font-medium">{host}</span>. Only sign in if you trust that server.
              </span>
            </p>
          )}
          <label className="space-y-1.5">
            <span className="text-xs font-medium">API token</span>
            <input
              type="password"
              autoComplete="current-password"
              autoFocus
              value={token}
              onChange={(event) => setToken(event.target.value)}
              aria-invalid={error ? true : undefined}
              aria-describedby={describedBy || undefined}
              className="h-9 w-full rounded-md border bg-background px-2 font-mono text-sm"
            />
          </label>
          {error && (
            <p id="auth-dialog-error" role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <DialogFooter className="gap-2">
            <Button type="button" variant="outline" onClick={() => finish(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!token.trim() || submitting}>
              {submitting ? "Signing in…" : "Sign in"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
