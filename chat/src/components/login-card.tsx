"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Copy, KeyRound, RefreshCw } from "lucide-react";
import { useChat } from "./chat-provider";
import { ProcessedMessage } from "./processed-message";
import { getErrorMessage } from "@/lib/error-utils";
import { loginNote, loginTitle, loginWhere } from "@/lib/login";

// Copies text, also where the Clipboard API is missing (plain http).
async function copyText(text: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  const copied = document.execCommand("copy");
  area.remove();
  if (!copied) throw new Error("Copying isn't available here");
}

// How long "Checking…" shows after Check again: the agent starts in the
// background and the card goes away once it is logged in.
const CHECKING_MS = 5000;

// The agent can't work until someone logs it in (native transport). The
// login happens in a terminal on the machine AgentAPI runs on, so the card
// says what to run and where, docked above the composer like a request.
export function LoginCard() {
  const { loginRequired: login, loginWaitsAtStart, retryLogin } = useChat();
  const [copied, setCopied] = useState(false);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);
  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  const later = (fn: () => void, ms: number) => {
    timers.current.push(setTimeout(fn, ms));
  };

  if (!login) return null;
  const where = loginWhere(login);

  const copy = async () => {
    try {
      await copyText(login.command);
      setCopied(true);
      later(() => setCopied(false), 2000);
    } catch (failure) {
      setError(getErrorMessage(failure));
    }
  };

  const check = async () => {
    if (checking) return;
    setChecking(true);
    setError("");
    try {
      await retryLogin();
      later(() => setChecking(false), CHECKING_MS);
    } catch (failure) {
      setError(getErrorMessage(failure));
      setChecking(false);
    }
  };

  return (
    <section
      aria-label={loginTitle(login)}
      data-login-required
      className="decision-card overflow-hidden rounded-xl border border-state-needs/45 bg-card shadow-lg shadow-black/5"
    >
      <header className="px-3.5 pb-1 pt-3">
        <div className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.06em] text-state-needs">
          <KeyRound className="size-3.5" />
          <span>Login</span>
        </div>
        <h2 className="min-w-0 text-sm font-semibold leading-5 [overflow-wrap:anywhere]">{loginTitle(login)}</h2>
        {login.message && <p className="mt-0.5 text-[13px] text-muted-foreground">{login.message}</p>}
      </header>
      <div className="grid gap-2 px-3.5 pb-3 pt-1">
        <div>
          <p className="mb-1 text-[11.5px] text-muted-foreground">
            Run in a terminal{where ? <> <span className="font-medium text-foreground" data-login-where>{where}</span></> : null}:
          </p>
          <div className="flex items-stretch overflow-hidden rounded-md bg-term text-term-foreground">
            <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre px-3 py-2 font-mono text-xs leading-[1.55]">
              <span className="mr-2 text-term-foreground/60" aria-hidden="true">$</span>
              {login.command}
            </pre>
            <button
              type="button"
              onClick={() => void copy()}
              className="flex shrink-0 items-center gap-1 border-l border-term-foreground/15 px-2.5 text-[11px] text-term-foreground/75 outline-none transition hover:bg-term-foreground/10 hover:text-term-foreground focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
              aria-label={copied ? "Copied the command" : "Copy the command"}
            >
              {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
              <span className="hidden sm:inline">{copied ? "Copied" : "Copy"}</span>
            </button>
          </div>
        </div>
        {login.detail && (
          <div
            className="max-h-32 overflow-auto rounded-md border bg-background px-3 py-2 [&_.prose-transcript]:text-[13px] [&_.prose-transcript]:leading-[1.6]"
            data-login-detail
          >
            <ProcessedMessage messageContent={login.detail} isUser={false} renderMode="markdown" />
          </div>
        )}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 pt-0.5">
          <button
            type="button"
            onClick={() => void check()}
            disabled={checking}
            className="flex min-h-9 items-center gap-1.5 rounded-lg bg-state-needs px-3 text-[13px] font-semibold text-background outline-none transition hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
          >
            <RefreshCw className={`size-3.5 ${checking ? "animate-spin" : ""}`} />
            {checking ? "Checking…" : "Check again"}
          </button>
          <p className="min-w-0 flex-1 text-[11.5px] text-muted-foreground" role={error ? "alert" : "status"}>
            {error ? <span className="text-destructive">{error}</span> : loginNote(loginWaitsAtStart)}
          </p>
        </div>
      </div>
    </section>
  );
}
