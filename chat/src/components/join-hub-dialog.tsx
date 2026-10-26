"use client";

import {useEffect, useState} from "react";
import {CircleCheck, ExternalLink} from "lucide-react";
import {useChatActions} from "./chat-provider";
import {CopyCommand} from "./copy-command";
import {Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle} from "./ui/dialog";
import {hubAddress, joinCommands, joinedHub, loopback} from "@/lib/join-hub";

// JoinHubDialog shows how to add this AgentAPI to an AgentAPI hub, with
// the commands to copy: start a hub, join this machine to it, open it.
// The hub's address, typed in, goes into the commands. It says so when
// this AgentAPI already reports to a hub.
export function JoinHubDialog({open, onOpenChange}: {open: boolean; onOpenChange: (open: boolean) => void}) {
  const {getWebhook} = useChatActions();
  const [joined, setJoined] = useState<string>();
  const [address, setAddress] = useState("");
  useEffect(() => {
    if (!open) return;
    let active = true;
    getWebhook()
      .then((webhook) => {
        if (!active) return;
        const hub = joinedHub(webhook.url);
        setJoined(hub);
        // A loopback address is the hub's own machine: not one to open.
        if (hub && !loopback(hub)) setAddress((current) => current || hub);
      })
      .catch(() => {
        // Without the webhook settings, the steps still show.
      });
    return () => {
      active = false;
    };
  }, [open, getWebhook]);
  const hub = hubAddress(address);
  const commands = joinCommands(hub);
  const steps = [
    {
      title: "Run a hub",
      body: "Once, on a machine that stays on and that your other machines reach. It prints the secret for step 2 and the token you sign in with.",
      command: commands.serve,
    },
    {
      title: "Join this machine",
      body: "On this machine. Its AgentAPIs, this one included, join now and on every start. If the hub can't resolve this machine's name, put its address in place of {hostname}.",
      command: commands.join,
    },
  ];
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Add to an AgentAPI hub</DialogTitle>
          <DialogDescription>A hub lists the AgentAPIs of all your machines on one page and opens any of them here.</DialogDescription>
        </DialogHeader>
        {joined && (
          <p className="flex items-start gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-sm" role="status">
            <CircleCheck className="mt-0.5 size-4 shrink-0 text-state-ready" aria-hidden="true" />
            <span>
              This AgentAPI reports to the hub at <code className="font-mono text-[12.5px] [overflow-wrap:anywhere]">{joined}</code>.
            </span>
          </p>
        )}
        <label className="block space-y-1.5">
          <span className="text-sm font-medium">Hub address</span>
          <input
            value={address}
            onChange={(event) => setAddress(event.target.value)}
            placeholder="hub.vpn:3290"
            aria-label="Hub address"
            inputMode="url"
            className="h-10 w-full rounded-lg border bg-card px-3 font-mono text-sm shadow-xs outline-none placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/40"
          />
          <span className="block text-xs text-muted-foreground">Where your machines reach the hub. The commands below fill it in.</span>
        </label>
        <ol className="space-y-3">
          {steps.map((step, index) => (
            <li key={step.title} className="flex gap-3 rounded-xl border p-3.5">
              <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-foreground text-xs font-semibold text-background tabular-nums">{index + 1}</span>
              <div className="min-w-0 flex-1">
                <p className="text-sm font-semibold">{step.title}</p>
                <p className="mt-0.5 text-[13px] text-muted-foreground">{step.body}</p>
                <CopyCommand text={step.command} />
              </div>
            </li>
          ))}
          <li className="flex gap-3 rounded-xl border p-3.5">
            <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-foreground text-xs font-semibold text-background tabular-nums">3</span>
            <div className="min-w-0 flex-1">
              <p className="text-sm font-semibold">Open the hub</p>
              <p className="mt-0.5 text-[13px] text-muted-foreground">Sign in with the HUB_TOKEN step 1 printed. This agent is on the list.</p>
              {hub ? (
                <a
                  href={commands.open}
                  target="_blank"
                  rel="noreferrer"
                  className="mt-2 inline-flex items-center gap-1.5 font-mono text-[12.5px] font-medium underline underline-offset-4 [overflow-wrap:anywhere] hover:no-underline"
                >
                  {commands.open}
                  <ExternalLink className="size-3.5 shrink-0" aria-hidden="true" />
                </a>
              ) : (
                <p className="mt-2 font-mono text-[12.5px] text-muted-foreground">{commands.open}</p>
              )}
            </div>
          </li>
        </ol>
        <p className="text-xs text-muted-foreground">
          The hub and this machine need AgentAPI 1.3 or later: <code className="font-mono">sudo agentapi update</code>.
        </p>
      </DialogContent>
    </Dialog>
  );
}
