"use client";

import { useEffect, useRef, useState } from "react";
import { KeyRound, RefreshCw } from "lucide-react";
import { useChat } from "./chat-provider";
import { CardStatus, CommandBlock, DecisionCardShell, MarkdownDetail, PrimaryButton } from "./decision-card";
import { getErrorMessage } from "@/lib/error-utils";
import { loginNote, loginTitle, loginWhere } from "@/lib/login";

// How long "Checking…" shows after Check again: the agent starts in the
// background and the card goes away once it is logged in.
const CHECKING_MS = 5000;

// The agent can't work until someone logs it in (native transport). The
// login happens in a terminal on the machine AgentAPI runs on, so the card
// says what to run and where, docked above the composer like a request.
export function LoginCard() {
  const { loginRequired: login, agentStarting, retryLogin } = useChat();
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);

  if (!login) return null;
  const where = loginWhere(login);

  const check = async () => {
    if (checking) return;
    setChecking(true);
    setError("");
    try {
      await retryLogin();
      timer.current = setTimeout(() => setChecking(false), CHECKING_MS);
    } catch (failure) {
      setError(getErrorMessage(failure));
      setChecking(false);
    }
  };

  return (
    <DecisionCardShell data-login-required icon={KeyRound} label="Login" title={loginTitle(login)} description={login.message}>
      <div className="grid gap-2 px-3.5 pb-3 pt-1">
        <div>
          <p className="mb-1 text-[11.5px] text-muted-foreground">
            Run in a terminal{where ? <> <span className="font-medium text-foreground" data-login-where>{where}</span></> : null}:
          </p>
          <CommandBlock command={login.command} copyable />
        </div>
        {login.detail && <MarkdownDetail content={login.detail} small data-login-detail />}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 pt-0.5">
          <PrimaryButton onClick={() => void check()} disabled={checking} className="flex min-h-9 items-center gap-1.5 px-3">
            <RefreshCw className={`size-3.5 ${checking ? "animate-spin" : ""}`} />
            {checking ? "Checking…" : "Check again"}
          </PrimaryButton>
          <CardStatus className="min-w-0 flex-1 text-[11.5px]" status={loginNote(agentStarting)} error={error} />
        </div>
      </div>
    </DecisionCardShell>
  );
}
