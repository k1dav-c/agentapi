"use client";

import {ArrowRight, CircleCheck, ClipboardList, Gauge, KeyRound, MessageCircleQuestion, ShieldQuestion, SquareTerminal} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {CopyCommand} from "@/components/copy-command";
import {
  activity,
  displayName,
  formatAgo,
  formatElapsed,
  instanceState,
  type HubFilter,
  type HubInstance,
  type InstanceState,
} from "@/lib/hub";
import {cn} from "@/lib/utils";
import {Constellation} from "./brand";
import {StatusGlyph} from "./status-glyph";
import {agentName} from "./instance-row";

// What a waiting instance asks for: its icon and the word for it.
function ask(instance: HubInstance): {icon: typeof ShieldQuestion; label: string} {
  const summary = instance.summary;
  switch (summary?.attention) {
    case "login":
      return {icon: KeyRound, label: "Login"};
    case "limit":
      return {icon: Gauge, label: "Usage limit"};
    case "dialog":
      return {icon: SquareTerminal, label: "Terminal dialog"};
  }
  switch (summary?.request?.kind) {
    case "question":
      return {icon: MessageCircleQuestion, label: "Question"};
    case "plan":
      return {icon: ClipboardList, label: "Plan"};
    default:
      return {icon: ShieldQuestion, label: "Approval"};
  }
}

// What the card's button says: a request is answered, anything else is
// looked at.
function action(instance: HubInstance): string {
  return instance.summary?.attention === "request" || instance.summary?.attention === "dialog" ? "Answer" : "Open";
}

function greeting(now: number): string {
  const hour = new Date(now).getHours();
  return hour < 5 ? "Working late" : hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
}

function Meta({instance}: {instance: HubInstance}) {
  const agent = agentName(instance);
  return (
    <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
      {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3" />}
      <span className="shrink-0">{agent}</span>
      {instance.hostname && (
        <>
          <span aria-hidden="true" className="text-muted-foreground/40">·</span>
          <span className="min-w-0 truncate" data-hub-host>
            {instance.hostname}
          </span>
        </>
      )}
    </span>
  );
}

function Section({title, count, children}: {title: string; count: number; children: React.ReactNode}) {
  return (
    <section className="mt-10">
      <h3 className="flex items-center gap-2 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
        {title}
        <span className="text-muted-foreground/60 tabular-nums">{count}</span>
      </h3>
      <div className="mt-3 grid gap-3 md:grid-cols-2 2xl:grid-cols-3">{children}</div>
    </section>
  );
}

const cardClass =
  "group/card hub-rise flex min-w-0 flex-col rounded-2xl border bg-card p-4 text-left shadow-xs outline-none transition-[box-shadow,transform,border-color] hover:-translate-y-0.5 hover:shadow-md focus-visible:ring-[3px] focus-visible:ring-ring/50";

function NeedsCard({instance, now, onOpen}: {instance: HubInstance; now: number; onOpen: () => void}) {
  const {icon: Icon, label} = ask(instance);
  const since = activity(instance).since;
  return (
    <button type="button" onClick={onOpen} className={cn(cardClass, "border-state-needs/35 hover:border-state-needs/60")}>
      <span className="flex items-center gap-2">
        <span className="flex size-7 items-center justify-center rounded-lg bg-state-needs/12 text-state-needs">
          <Icon className="size-4" aria-hidden="true" />
        </span>
        <span className="text-xs font-semibold tracking-wide text-state-needs uppercase">{label}</span>
        {since && (
          <span className="ml-auto text-xs text-muted-foreground tabular-nums">
            {formatAgo(since, now) === "now" ? "just now" : `waiting ${formatAgo(since, now)}`}
          </span>
        )}
      </span>
      <span className="mt-3 line-clamp-2 text-[15px] leading-snug font-medium text-pretty">{activity(instance).text}</span>
      <span className="mt-auto flex items-end justify-between gap-3 pt-4">
        <span className="min-w-0">
          <span className="block truncate text-sm font-medium">{displayName(instance)}</span>
          <Meta instance={instance} />
        </span>
        <span className="flex shrink-0 items-center gap-1 rounded-lg bg-foreground px-3 py-1.5 text-sm font-medium text-background transition-transform group-hover/card:translate-x-0.5">
          {action(instance)} <ArrowRight className="size-3.5" aria-hidden="true" />
        </span>
      </span>
    </button>
  );
}

function WorkingCard({instance, now, onOpen}: {instance: HubInstance; now: number; onOpen: () => void}) {
  const doing = activity(instance);
  return (
    <button type="button" onClick={onOpen} className={cardClass}>
      <span className="flex items-baseline justify-between gap-3">
        <span className="min-w-0 truncate text-sm font-medium">{displayName(instance)}</span>
        <span className="shrink-0 font-mono text-sm text-state-working tabular-nums">{formatElapsed(doing.since, now)}</span>
      </span>
      <span className="mt-1.5 line-clamp-2 text-sm text-muted-foreground">{doing.text}</span>
      <span className="hub-shimmer mt-4 block h-1 rounded-full bg-state-working/12" aria-hidden="true" />
      <span className="mt-3">
        <Meta instance={instance} />
      </span>
    </button>
  );
}

function FinishedCard({instance, now, onOpen}: {instance: HubInstance; now: number; onOpen: () => void}) {
  const doing = activity(instance);
  return (
    <button type="button" onClick={onOpen} className={cardClass}>
      <span className="flex items-center gap-2">
        <CircleCheck className="size-4 text-state-ready" aria-hidden="true" />
        <span className="min-w-0 truncate text-sm font-medium">{displayName(instance)}</span>
        <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">{formatAgo(doing.since, now)}</span>
      </span>
      <span className="mt-2 line-clamp-2 text-sm text-muted-foreground">{doing.text}</span>
      <span className="mt-3">
        <Meta instance={instance} />
      </span>
    </button>
  );
}

const stats: {filter: HubFilter; label: string; tone: string; tint: string; glyph: InstanceState}[] = [
  {filter: "needs-you", label: "Need you", tone: "text-state-needs", tint: "bg-state-needs/6 border-state-needs/30", glyph: "needs-you"},
  {filter: "working", label: "Working", tone: "text-state-working", tint: "bg-state-working/5 border-state-working/25", glyph: "working"},
  {filter: "unread", label: "Finished, unseen", tone: "text-state-ready", tint: "bg-state-ready/5 border-state-ready/25", glyph: "unread"},
  {filter: "offline", label: "Offline", tone: "text-state-fault", tint: "bg-state-fault/5 border-state-fault/25", glyph: "unreachable"},
];

// Overview is the main pane while no instance is open: what needs you,
// what runs, and what finished since you last looked.
export function Overview({
  root,
  instances,
  counts,
  now,
  onOpen,
  onFilter,
  onAdd,
}: {
  root: string;
  instances: HubInstance[];
  counts: Record<HubFilter, number>;
  now: number;
  onOpen: (id: string) => void;
  onFilter: (filter: HubFilter) => void;
  onAdd: () => void;
}) {
  const by = (state: string) => instances.filter((instance) => instanceState(instance) === state);
  const needs = by("needs-you");
  const working = by("working").sort((a, b) => (a.summary?.task?.started_at ?? "").localeCompare(b.summary?.task?.started_at ?? ""));
  const unread = by("unread");
  if (instances.length === 0) {
    return (
      <div className="h-full overflow-y-auto">
        <div className="mx-auto max-w-2xl px-10 pt-12 pb-16">
          <p className="text-sm text-muted-foreground">{greeting(now)}</p>
          <h2 className="mt-2 text-4xl font-semibold tracking-tight">Connect your first agent</h2>
          <p className="mt-3 text-[15px] text-muted-foreground">Each machine joins once. Its agents show up here and stay after restarts.</p>
          <Onboarding root={root} onAdd={onAdd} />
        </div>
      </div>
    );
  }
  const headline =
    needs.length > 0
        ? `${needs.length} ${needs.length === 1 ? "agent needs" : "agents need"} you`
        : working.length > 0
          ? "Everything's running"
          : "All quiet";
  const sub = [
          working.length && `${working.length} working`,
          unread.length && `${unread.length} finished since you looked`,
          counts.offline && `${counts.offline} offline`,
    `${instances.length} in all`,
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-6xl px-10 pt-12 pb-16">
        <p className="text-sm text-muted-foreground">
          {greeting(now)} · {new Date(now).toLocaleDateString(undefined, {weekday: "long", month: "long", day: "numeric"})}
        </p>
        <h2 className={cn("mt-2 text-4xl font-semibold tracking-tight text-balance", needs.length > 0 && "text-state-needs")}>{headline}</h2>
        <p className="mt-3 text-[15px] text-muted-foreground">{sub}</p>

        <div className="mt-8 grid grid-cols-4 gap-3">
          {stats.map((stat) => (
            <button
              key={stat.filter}
              type="button"
              onClick={() => onFilter(stat.filter)}
              aria-label={`${counts[stat.filter]} ${stat.label}: show them in the list`}
              className={cn(
                "flex items-start justify-between rounded-2xl border bg-card px-4 py-3.5 text-left shadow-xs outline-none transition-shadow hover:shadow-md focus-visible:ring-[3px] focus-visible:ring-ring/50",
                counts[stat.filter] > 0 && stat.tint,
              )}
            >
              <span>
                <span className={cn("block text-3xl font-semibold tracking-tight tabular-nums", counts[stat.filter] > 0 ? stat.tone : "text-muted-foreground/50")}>
                  {counts[stat.filter]}
                </span>
                <span className="mt-0.5 block text-xs text-muted-foreground">{stat.label}</span>
              </span>
              <StatusGlyph state={counts[stat.filter] > 0 ? stat.glyph : "idle"} className="mt-1 size-4" />
            </button>
          ))}
        </div>

        {needs.length > 0 && (
          <Section title="Needs you" count={needs.length}>
            {needs.map((instance) => (
              <NeedsCard key={instance.id} instance={instance} now={now} onOpen={() => onOpen(instance.id)} />
            ))}
          </Section>
        )}
        {working.length > 0 && (
          <Section title="Working" count={working.length}>
            {working.map((instance) => (
              <WorkingCard key={instance.id} instance={instance} now={now} onOpen={() => onOpen(instance.id)} />
            ))}
          </Section>
        )}
        {unread.length > 0 && (
          <Section title="Finished since you looked" count={unread.length}>
            {unread.map((instance) => (
              <FinishedCard key={instance.id} instance={instance} now={now} onOpen={() => onOpen(instance.id)} />
            ))}
          </Section>
        )}
        {needs.length === 0 && working.length === 0 && unread.length === 0 && (
          <div className="mt-16 flex flex-col items-center text-center">
            <Constellation className="w-56 opacity-80" />
            <p className="mt-6 text-sm text-muted-foreground">Nothing waits for you. Pick an agent to look in on it.</p>
          </div>
        )}
      </div>
    </div>
  );
}


// Onboarding shows how to connect the first agents: once per machine.
export function Onboarding({root, onAdd, compact = false}: {root: string; onAdd: () => void; compact?: boolean}) {
  const steps = [
    {title: "Update AgentAPI on each machine", body: "The hub needs AgentAPI 1.3 or later.", command: "sudo agentapi update"},
    {
      title: "Join this hub",
      body: "Once per machine. It writes the hub into ~/.agentapi/config.yaml, so the agents join on every start.",
      command: `agentapi hub join ${root}/hooks --secret "$HUB_SECRET" --public-url 'http://<this machine>:{port}'`,
    },
    {title: "Restart the agents once", body: "Agents running already show up within a second; ones started before 1.2 join on their next start.", command: ""},
  ];
  return (
    <div className={cn("hub-rise", compact ? "px-4 py-6" : "")}>
      {compact && (
        <>
          <p className="text-base font-semibold tracking-tight">Connect your first agent</p>
          <p className="mt-1 text-sm text-muted-foreground">Each machine joins once; its agents stay after restarts.</p>
        </>
      )}
      <ol className={cn("space-y-3", compact ? "mt-5" : "mt-8")}>
        {steps.map((step, i) => (
          <li key={step.title} className="flex gap-3.5 rounded-2xl border bg-card p-4 shadow-xs">
            <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-foreground text-xs font-semibold text-background tabular-nums">{i + 1}</span>
            <div className="min-w-0 flex-1">
              <p className="text-sm font-semibold">{step.title}</p>
              <p className="mt-0.5 text-sm text-muted-foreground">{step.body}</p>
              {step.command && <CopyCommand text={step.command} />}
            </div>
          </li>
        ))}
      </ol>
      <p className="mt-5 text-sm text-muted-foreground">
        Can&apos;t run <code className="font-mono text-[12.5px]">hub join</code> there?{" "}
        <button type="button" onClick={onAdd} className="font-medium text-foreground underline underline-offset-4 hover:no-underline">
          Add an agent by its URL
        </button>
        .
      </p>
    </div>
  );
}
