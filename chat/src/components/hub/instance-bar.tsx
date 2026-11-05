"use client";

import {useEffect} from "react";
import {ArrowLeft, ExternalLink, X} from "lucide-react";
import {AgentMark} from "@/components/agent-mark";
import {Button} from "@/components/ui/button";
import {chatURL, displayName, instanceState, instanceVersion, stateLabel, type HubInstance} from "@/lib/hub";
import {frameInstance, toFrame, type MachineAgent} from "@/lib/hub-frame";
import {shownUpdate} from "@/lib/hub-update";
import {cn} from "@/lib/utils";
import {agentName} from "./instance-row";
import {MachineSwitch} from "./machine-tabs";
import {StatusGlyph, stateTone} from "./status-glyph";
import {cancelFromHub} from "./update";
import {UpdateStatus} from "./update-chip";
import {VersionTag} from "./version-tag";

// Where the chat is served, below the hub.
export const basePath = (process.env.NEXT_PUBLIC_BASE_PATH ?? "/chat").replace(/\/$/, "");

// FrameInfo tells the open instance's chat what its bar shows, whenever
// that changes and whenever the chat says it is ready (ready counts it):
// machine is the agents of its machine, for the bar's switch.
export function FrameInfo({
  frame,
  ready,
  instance,
  machine,
  hubVersion,
  root,
}: {
  frame: React.RefObject<HTMLIFrameElement | null>;
  ready: number;
  instance: HubInstance;
  machine: MachineAgent[];
  hubVersion: string;
  root: string;
}) {
  const info = JSON.stringify(toFrame(frameInstance(instance, {hubVersion, agent: agentName(instance), tab: chatURL(basePath, root, instance.id), machine})));
  useEffect(() => {
    frame.current?.contentWindow?.postMessage(JSON.parse(info), window.location.origin);
  }, [frame, info, ready]);
  return null;
}

// InstanceBar heads the open instance: back to the list or overview, what
// it is and does, the other agents of its machine (machine; onOpen opens
// one), and its own chat in a tab of its own.
export function InstanceBar({
  instance,
  machine,
  hubVersion,
  root,
  onBack,
  onOpen,
}: {
  instance: HubInstance;
  machine: MachineAgent[];
  hubVersion: string;
  root: string;
  onBack: () => void;
  onOpen: (id: string) => void;
}) {
  const state = instanceState(instance);
  const agent = agentName(instance);
  const version = instanceVersion(instance);
  const update = shownUpdate(instance);
  return (
    <div className="box-content flex h-12 shrink-0 items-center gap-1 border-b bg-background px-1.5 pt-[env(safe-area-inset-top)] sm:h-14 md:gap-2 md:px-3">
      <Button variant="ghost" size="icon" className="size-10 shrink-0 rounded-full md:hidden" onClick={onBack} aria-label="Back to Agents">
        <ArrowLeft className="size-5" />
      </Button>
      <Button variant="ghost" size="icon" className="hidden size-9 rounded-full md:inline-flex" onClick={onBack} aria-label="Back to the overview" title="Back to the overview (esc)">
        <X className="size-4" />
      </Button>
      <StatusGlyph state={state} className="ml-1 hidden sm:block" />
      <div className="min-w-0 flex-1 pl-0.5 md:pl-1">
        <p className="truncate text-sm leading-tight font-semibold">{displayName(instance)}</p>
        <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
          <span className={cn("shrink-0 font-medium", stateTone[state])}>{stateLabel(instance)}</span>
          {update && <UpdateStatus update={update} onCancel={() => void cancelFromHub(root, instance.id)} />}
          {agent && (
            <>
              <span aria-hidden="true" className="text-muted-foreground/40">·</span>
              {instance.agent_type && <AgentMark agentType={instance.agent_type} className="size-3" />}
              <span className="shrink-0">{agent}</span>
            </>
          )}
          {instance.hostname && (
            <>
              <span aria-hidden="true" className="hidden text-muted-foreground/40 sm:inline">·</span>
              <span className="hidden min-w-0 truncate sm:inline" data-hub-host>
                {instance.hostname}
              </span>
            </>
          )}
          {version && (
            <>
              <span aria-hidden="true" className="hidden text-muted-foreground/40 sm:inline">·</span>
              <VersionTag version={version} hubVersion={hubVersion} lastKnown={instance.connection !== "connected"} className="hidden sm:flex" />
            </>
          )}
        </p>
      </div>
      <MachineSwitch agents={machine} current={instance.id} onOpen={onOpen} />
      <Button variant="ghost" size="sm" className="hidden h-9 shrink-0 gap-1.5 text-muted-foreground sm:inline-flex" asChild>
        <a href={chatURL(basePath, root, instance.id)} target="_blank" rel="noreferrer" aria-label={`Open ${displayName(instance)} in a new tab`}>
          <ExternalLink className="size-4" />
          <span className="hidden lg:inline">New tab</span>
        </a>
      </Button>
    </div>
  );
}
