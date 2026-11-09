"use client";

import {useState} from "react";
import {Check, Copy, SendHorizontal} from "lucide-react";
import TextareaAutosize from "react-textarea-autosize";
import {AgentMark} from "@/components/agent-mark";
import {Button} from "@/components/ui/button";
import {agentLabel, type DispatchJob} from "@/lib/hub-dispatch";
import {formatRunDuration, jobTotals} from "@/lib/hub-issues";
import {formatTokens} from "@/lib/hub-spend";
import {useCopy} from "@/lib/use-copy";
import {cn} from "@/lib/utils";
import {useHubTime} from "../clock";
import {Money} from "./money";

// The parts of an issue's detail below its status: a line to copy, the
// job's totals, the line to its agent, and the issue's description.

export function CopyLine({label, value, className}: {label: string; value: string; className?: string}) {
  const {copied, copy} = useCopy();
  return (
    <div className={cn("flex min-w-0 items-center gap-3 py-2", className)}>
      <dt className="w-20 shrink-0 text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 truncate font-mono text-[12.5px]" title={value}>
        {value}
      </dd>
      <button
        type="button"
        onClick={() => void copy(value)}
        aria-label={copied ? "Copied" : `Copy the ${label.toLowerCase()}`}
        className="flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        {copied ? <Check className="size-3.5 text-state-ready" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

// Totals are what the job's runs add up to.
export function Totals({job}: {job: DispatchJob}) {
  const now = useHubTime((n) => Math.floor(n / 15_000) * 15_000);
  const totals = jobTotals(job, now);
  const stats = [
    {label: "Spend", value: <Money usd={totals.usd} />, title: "API-equivalent spend of all its runs"},
    {label: totals.runs === 1 ? "Run" : "Runs", value: String(totals.runs)},
    {label: "Agent time", value: formatRunDuration(totals.agentSeconds), title: "How long its agents worked on tasks"},
    {label: "Tokens", value: formatTokens(totals.tokens)},
  ];
  return (
    <dl className="grid grid-cols-4 divide-x rounded-2xl border bg-card shadow-xs">
      {stats.map((stat) => (
        <div key={stat.label} className="min-w-0 px-3 py-2.5" title={stat.title}>
          <dt className="truncate text-[11px] text-muted-foreground">{stat.label}</dt>
          <dd className="mt-0.5 truncate text-[17px] font-semibold tracking-tight tabular-nums">{stat.value}</dd>
        </div>
      ))}
    </dl>
  );
}

// Composer sends the job's agent a message, as a review comment would.
export function Composer({job, busy, onSend}: {job: DispatchJob; busy: boolean; onSend: (message: string) => Promise<boolean>}) {
  const [text, setText] = useState("");
  const submit = async () => {
    const message = text.trim();
    if (!message || busy) return;
    if (await onSend(message)) setText("");
  };
  return (
    <form
      className="flex items-end gap-2 rounded-2xl border bg-card p-1.5 pl-3.5 shadow-sm transition-[border-color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/30"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <AgentMark agentType={job.agent} className="mb-2.5 size-3.5" />
      <TextareaAutosize
        minRows={1}
        maxRows={6}
        value={text}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            void submit();
          }
        }}
        placeholder={`Message ${agentLabel(job.agent)}…`}
        aria-label={`Message to ${agentLabel(job.agent)}`}
        className="min-w-0 flex-1 resize-none bg-transparent py-2 text-sm outline-none placeholder:text-muted-foreground/80"
      />
      <Button type="submit" size="icon" className="size-9 shrink-0 rounded-xl" disabled={busy || !text.trim()} aria-label="Send" title="Send (⌘↵)">
        <SendHorizontal className="size-4" />
      </Button>
    </form>
  );
}

// Body is the issue's description, folded past a few lines.
export function Body({text}: {text: string}) {
  const [open, setOpen] = useState(false);
  const long = text.length > 420 || text.split("\n").length > 8;
  return (
    <div>
      <p className={cn("text-sm leading-relaxed whitespace-pre-wrap text-foreground/85 [overflow-wrap:anywhere]", long && !open && "line-clamp-8")}>
        {/* Code spans read as code; the rest as it was written. */}
        {text.split(/(`[^`\n]+`)/).map((part, i) =>
          part.length > 2 && part.startsWith("`") && part.endsWith("`") ? (
            <code key={i} className="rounded bg-muted px-1 py-px font-mono text-[0.88em]">
              {part.slice(1, -1)}
            </code>
          ) : (
            part
          ),
        )}
      </p>
      {long && (
        <button type="button" onClick={() => setOpen(!open)} className="mt-1 text-xs font-medium text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
          {open ? "Less" : "More"}
        </button>
      )}
    </div>
  );
}
