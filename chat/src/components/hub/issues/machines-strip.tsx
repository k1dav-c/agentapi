"use client";

import {useCallback, useEffect, useRef, useState} from "react";
import {AgentMark} from "@/components/agent-mark";
import {agentLabel, type DispatchHost} from "@/lib/hub-dispatch";
import {formatAgo} from "@/lib/hub-format";
import {cn} from "@/lib/utils";
import {useHubTime} from "../clock";
import {Capacity} from "./assign-menu";

function Machine({host}: {host: DispatchHost}) {
  const seen = useHubTime((now) => formatAgo(host.last_seen, now));
  const agents = host.agents ?? [];
  const status = !host.online ? `offline${seen ? ` ${seen}` : ""}` : host.active >= host.max_jobs ? "full" : `${host.max_jobs - host.active} free`;
  return (
    <li
      className={cn("flex shrink-0 snap-start items-center gap-2.5 rounded-full border bg-card py-1 pr-3 pl-2 text-xs shadow-xs", !host.online && "border-dashed bg-transparent shadow-none")}
      title={[`${host.hostname}: ${host.online ? `${host.active} of ${host.max_jobs} jobs` : "offline"}`, `Runs ${agents.map(agentLabel).join(", ") || "no agent"}`, ...(host.repos ?? []).map((repo) => `· ${repo}`)].join("\n")}
    >
      <span className={cn("size-1.5 rounded-full", host.online ? "bg-state-ready" : "bg-muted-foreground/40")} aria-hidden="true" />
      <span className={cn("font-mono text-[12px]", !host.online && "text-muted-foreground")}>{host.hostname}</span>
      <Capacity host={host} />
      <span className="flex items-center gap-1" aria-label={`Runs ${agents.map(agentLabel).join(", ")}`}>
        {agents.map((agent) => (
          <AgentMark key={agent} agentType={agent} className={cn("size-3", !host.online && "opacity-45 grayscale")} />
        ))}
      </span>
      <span className={cn("tabular-nums", host.online ? "text-muted-foreground" : "text-muted-foreground/80")}>{status}</span>
    </li>
  );
}

// The edges of a strip that scrolls, faded where there is more.
const fades: Record<string, string> = {
  none: "",
  start: "[mask-image:linear-gradient(90deg,transparent,#000_28px)]",
  end: "[mask-image:linear-gradient(90deg,#000_calc(100%-64px),transparent)]",
  both: "[mask-image:linear-gradient(90deg,transparent,#000_28px,#000_calc(100%-64px),transparent)]",
};

// MachinesStrip is the machines whose launchers report: online or not,
// their jobs against their room, and the agents each runs. On a phone it
// is one row that scrolls sideways, by touch or, focused, by the arrow
// keys, faded at the edge there is more past; wider, it wraps.
export function MachinesStrip({hosts}: {hosts: DispatchHost[]}) {
  const sorted = [...hosts].sort((a, b) => Number(b.online) - Number(a.online) || a.hostname.localeCompare(b.hostname));
  const strip = useRef<HTMLElement>(null);
  const [more, setMore] = useState<keyof typeof fades>("none");
  const measure = useCallback(() => {
    const el = strip.current;
    if (!el) return;
    const start = el.scrollLeft > 2;
    const end = el.scrollLeft + el.clientWidth < el.scrollWidth - 2;
    setMore(start && end ? "both" : start ? "start" : end ? "end" : "none");
  }, []);
  useEffect(() => {
    measure();
    const el = strip.current;
    if (!el) return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [measure, hosts.length]);
  const scrolls = more !== "none";
  return (
    <section
      ref={strip}
      aria-label="Machines"
      onScroll={measure}
      // A row that scrolls takes the focus, so the arrow keys move it.
      tabIndex={scrolls ? 0 : undefined}
      className={cn(
        "-mx-5 snap-x snap-mandatory scroll-px-5 overflow-x-auto overscroll-x-contain px-5 py-1 outline-none [scrollbar-width:none] focus-visible:ring-[3px] focus-visible:ring-ring/50 sm:mx-0 sm:px-0 sm:[mask-image:none]",
        fades[more],
      )}
    >
      <ul className="flex w-max gap-2 sm:w-auto sm:flex-wrap">
        {sorted.map((host) => (
          <Machine key={host.hostname} host={host} />
        ))}
      </ul>
    </section>
  );
}
