"use client";

import {useEffect, useState} from "react";
import {ArrowLeft, ExternalLink, X} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {Button} from "@/components/ui/button";
import {HEADER_ACTIONS_ID} from "@/app/header";
import {fromFrame, instanceFromHub, openFromFrame, type FrameInstance} from "@/lib/hub-frame";
import {cn} from "@/lib/utils";
import {MachineSwitch} from "./machine-tabs";
import {StatusGlyph, stateTone} from "./status-glyph";
import {UpdateStatus} from "./update-chip";
import {VersionTag} from "./version-tag";

// FrameBar heads a chat the hub opens: back to the hub's list or overview,
// what the instance is and does (the hub tells it), the other agents of its
// machine, which the hub opens in its place, and the chat's own actions in
// the same bar, so a phone keeps its height for the conversation.
export function FrameBar() {
  const [instance, setInstance] = useState<FrameInstance>();
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const told = instanceFromHub(event, window.parent);
      if (told) setInstance(told);
    };
    window.addEventListener("message", onMessage);
    // On every load of the chat, a reload too: the hub answers each.
    window.parent.postMessage(fromFrame("ready"), window.location.origin);
    return () => window.removeEventListener("message", onMessage);
  }, []);
  const back = () => window.parent.postMessage(fromFrame("back"), window.location.origin);
  return (
    <div className="flex h-12 shrink-0 items-center gap-1 border-b bg-background px-1.5 sm:h-14 md:gap-2 md:px-3">
      <Button variant="ghost" size="icon" className="size-10 shrink-0 rounded-full md:hidden" onClick={back} aria-label="Back to Agents">
        <ArrowLeft className="size-5" />
      </Button>
      <Button variant="ghost" size="icon" className="hidden size-9 shrink-0 rounded-full md:inline-flex" onClick={back} aria-label="Back to the overview" title="Back to the overview">
        <X className="size-4" />
      </Button>
      {instance && <StatusGlyph state={instance.state} className="ml-1 hidden sm:block" />}
      <div className="min-w-0 flex-1 pl-0.5 md:pl-1">
        <p className="truncate text-sm leading-tight font-semibold">{instance?.name ?? " "}</p>
        <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
          {instance && <span className={cn("min-w-0 truncate font-medium", stateTone[instance.state])}>{instance.label}</span>}
          {instance?.update && <UpdateStatus update={instance.update} onCancel={() => window.parent.postMessage(fromFrame("cancel-update"), window.location.origin)} />}
          {instance?.agent && (
            <>
              <span aria-hidden="true" className="text-muted-foreground/40 max-[400px]:hidden">·</span>
              {instance.agentType && <AgentMark agentType={instance.agentType} className="size-3 shrink-0 max-[400px]:hidden" />}
              <span className="shrink-0 max-[400px]:hidden">{instance.agent}</span>
            </>
          )}
          {instance?.hostname && (
            <>
              <span aria-hidden="true" className="hidden text-muted-foreground/40 lg:inline">·</span>
              <span className="hidden min-w-0 truncate lg:inline" data-hub-host>
                {instance.hostname}
              </span>
            </>
          )}
          {instance?.version && (
            <>
              <span aria-hidden="true" className="hidden text-muted-foreground/40 lg:inline">·</span>
              <VersionTag version={instance.version} hubVersion={instance.hubVersion} lastKnown={instance.lastKnown} className="hidden lg:flex" />
            </>
          )}
        </p>
      </div>
      {instance?.machine && (
        <MachineSwitch agents={instance.machine} current={instance.id} onOpen={(id) => window.parent.postMessage(openFromFrame(id), window.location.origin)} />
      )}
      {/* The chat's actions (search, terminal, Explorer) render here. */}
      <div id={HEADER_ACTIONS_ID} className="flex shrink-0 items-center gap-0.5" />
      {instance && (
        <Button variant="ghost" size="sm" className="hidden h-9 shrink-0 gap-1.5 text-muted-foreground sm:inline-flex" asChild>
          <a href={instance.tab} target="_blank" rel="noreferrer" aria-label={`Open ${instance.name} in a new tab`}>
            <ExternalLink className="size-4" />
            <span className="hidden lg:inline">New tab</span>
          </a>
        </Button>
      )}
    </div>
  );
}
