"use client";

import {useCallback, useEffect, useState} from "react";
import {GitFork, History, LoaderCircle, RefreshCw} from "lucide-react";
import {toast} from "sonner";
import {useChatActions, useChatSelector} from "./chat-provider";
import {Button} from "./ui/button";
import type {SessionCost, SessionSummary} from "@/lib/chat-api";
import {costDetails, formatUSD, periodTotals} from "@/lib/cost";
import {agentDisplayName} from "@/lib/agents";
import {getErrorMessage} from "@/lib/error-utils";
import {formatMessageTime} from "@/lib/format-time";

// An action on a session that asks for a second click: it replaces the
// conversation.
interface Pending {
  id: string;
  fork: boolean;
}

// The agent's sessions in its working directory, read from its session
// logs, to open one in place of this conversation or to start a copy of
// one (this one too). active says the list shows; it loads then.
// onOpened is called once a session opened.
export function SessionHistory({active, onOpened}: {active: boolean; onOpened: () => void}) {
  const serverStatus = useChatSelector((chat) => chat.serverStatus);
  const agentType = useChatSelector((chat) => chat.agentType);
  const {listSessions, resumeSession} = useChatActions();
  const [sessions, setSessions] = useState<SessionSummary[] | null>(null);
  const [current, setCurrent] = useState<string | undefined>();
  const [total, setTotal] = useState<SessionCost | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [pending, setPending] = useState<Pending | null>(null);
  const [opening, setOpening] = useState<Pending | null>(null);
  const working = serverStatus === "running";

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const list = await listSessions();
      setSessions(list.sessions);
      setCurrent(list.current);
      setTotal(list.total_cost ?? null);
    } catch (failure) {
      setError(getErrorMessage(failure));
    } finally {
      setLoading(false);
    }
  }, [listSessions]);

  useEffect(() => {
    if (active) void load();
  }, [active, load]);

  // The second click must come soon.
  useEffect(() => {
    if (!pending) return;
    const timer = window.setTimeout(() => setPending(null), 4000);
    return () => window.clearTimeout(timer);
  }, [pending]);

  const choose = async (session: SessionSummary, fork: boolean) => {
    if (!pending || pending.id !== session.id || pending.fork !== fork) {
      setPending({id: session.id, fork});
      return;
    }
    setPending(null);
    setOpening({id: session.id, fork});
    try {
      await resumeSession(session.id, fork);
      toast.success(fork ? "Started a copy of the session" : "Opened the session", {description: sessionTitle(session)});
      onOpened();
    } catch (failure) {
      toast.error(fork ? "Couldn't copy the session" : "Couldn't open the session", {description: getErrorMessage(failure)});
    } finally {
      setOpening(null);
    }
  };

  return (
    <div className="space-y-3" data-session-history>
      <div className="flex items-center justify-between gap-3">
        <h3 className="text-sm font-semibold">Sessions</h3>
        <Button type="button" variant="ghost" size="sm" disabled={loading} onClick={() => void load()}>
          <RefreshCw className={loading ? "animate-spin" : undefined} />
          Refresh
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        {agentDisplayName(agentType, "The agent")}&apos;s sessions in this directory. Open one to go on with it here, or start a
        copy to try something else and leave the session as it is. Either replaces this conversation and its queue.
      </p>
      {total && total.calls > 0 && <SpendCard total={total} sessions={sessions?.length ?? 0} />}
      {working && <p className="text-xs text-state-needs">The agent is working: stop it or wait until it is done to switch sessions.</p>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      {sessions === null && loading && (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <LoaderCircle className="size-4 animate-spin" /> Reading the sessions…
        </p>
      )}
      {sessions?.length === 0 && (
        <p className="rounded-xl border border-dashed p-6 text-center text-sm text-muted-foreground">No sessions in this directory yet.</p>
      )}
      <ul className="space-y-2">
        {sessions?.map((session) => {
          const isCurrent = session.id === current;
          const busy = opening?.id === session.id;
          const confirmOpen = pending?.id === session.id && !pending.fork;
          const confirmFork = pending?.id === session.id && pending.fork;
          return (
            <li key={session.id} className="rounded-xl border p-3" data-session={session.id}>
              <div className="flex items-start gap-2">
                <History className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium" title={sessionTitle(session)}>
                    {sessionTitle(session)}
                  </p>
                  {session.title && <p className="truncate text-xs text-muted-foreground">{session.first_prompt}</p>}
                  <p className="mt-0.5 text-[11px] text-muted-foreground">
                    {session.prompts} {session.prompts === 1 ? "message" : "messages"} · {formatMessageTime(session.updated_at)} ·{" "}
                    <span className="font-mono" title={session.id}>{session.id.slice(0, 8)}</span>
                    {session.cost && session.cost.calls > 0 && (
                      <>
                        {" · "}
                        <span className="tabular-nums" title={costDetails(session.cost, unpriced(session.cost))} data-session-cost>
                          {formatUSD(session.cost.usd)}
                        </span>
                      </>
                    )}
                    {isCurrent && <span className="ml-1.5 rounded-full bg-muted px-1.5 py-0.5 font-medium text-foreground">Open now</span>}
                  </p>
                </div>
              </div>
              <div className="mt-2 flex flex-wrap justify-end gap-2">
                {!isCurrent && (
                  <Button
                    type="button"
                    size="sm"
                    variant={confirmOpen ? "destructive" : "outline"}
                    disabled={working || opening !== null}
                    onClick={() => void choose(session, false)}
                  >
                    {busy && !opening?.fork && <LoaderCircle className="animate-spin" />}
                    {confirmOpen ? "Click again to open" : "Open"}
                  </Button>
                )}
                <Button
                  type="button"
                  size="sm"
                  variant={confirmFork ? "destructive" : "outline"}
                  disabled={working || opening !== null}
                  onClick={() => void choose(session, true)}
                  title="Start a new session that begins as a copy of this one"
                >
                  {busy && opening?.fork ? <LoaderCircle className="animate-spin" /> : <GitFork />}
                  {confirmFork ? "Click again to copy" : "Start a copy"}
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// What the sessions in the directory spent over time, subagents included,
// in the viewer's days.
function SpendCard({total, sessions}: {total: SessionCost; sessions: number}) {
  const periods = periodTotals(total.hourly);
  const cells: [string, number][] = [
    ["Today", periods.today],
    ["7 days", periods.week],
    ["30 days", periods.month],
    ["All", periods.all],
  ];
  return (
    <section className="rounded-xl border p-3" aria-label="Spending" data-spend-card>
      <div className="grid grid-cols-4 gap-2">
        {cells.map(([label, usd]) => (
          <div key={label} className="min-w-0">
            <p className="text-[11px] text-muted-foreground">{label}</p>
            <p className="truncate text-sm font-semibold tabular-nums">{formatUSD(usd)}</p>
          </div>
        ))}
      </div>
      <p className="mt-2 text-[11px] text-muted-foreground" title={costDetails(total, unpriced(total))}>
        API-equivalent cost of {sessions} {sessions === 1 ? "session" : "sessions"} and their subagents at list prices; a
        subscription doesn&apos;t charge it.
        {unpriced(total) && " Models without a known price count tokens only."}
      </p>
    </section>
  );
}

// Some calls were to a model without a known price.
function unpriced(cost: SessionCost): boolean {
  return cost.models.some((model) => !model.priced);
}

// What a session is called: its title, else its first message.
function sessionTitle(session: SessionSummary): string {
  return session.title || session.first_prompt || session.id;
}
