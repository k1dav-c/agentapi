"use client";

import {useEffect, useRef, useState, type FormEvent} from "react";
import {KeyRound} from "lucide-react";
import {Button} from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {exchangeToken, setSignInHandler} from "@/lib/auth";

interface Prompt {
  baseURL: string;
  resolve: (signedIn: boolean) => void;
}

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
      if (await exchangeToken(prompt.baseURL, token.trim())) {
        finish(true);
      } else {
        setError("The server did not accept this token.");
      }
    } catch {
      setError("Could not reach the server.");
    } finally {
      setSubmitting(false);
    }
  };

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
              This server was started with <code className="font-mono text-xs">--api-token</code>.
              Enter the token to continue. It is exchanged for a session cookie and not stored by the page.
            </DialogDescription>
          </DialogHeader>
          <label className="space-y-1.5">
            <span className="text-xs font-medium">API token</span>
            <input
              type="password"
              autoComplete="current-password"
              autoFocus
              value={token}
              onChange={(event) => setToken(event.target.value)}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? "auth-dialog-error" : undefined}
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
