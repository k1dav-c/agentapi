"use client";

import {useCallback, useMemo, useRef, useState} from "react";
import {chatURL, displayName, type HubInstance} from "@/lib/hub";
import type {Favorite} from "@/lib/hub-favorites";
import {frameOrder, KEPT_FRAMES, KEPT_FRAMES_PHONE, keptFrames} from "@/lib/hub-frames";
import {useMediaQuery} from "@/lib/use-media-query";
import {cn} from "@/lib/utils";
import {basePath, FrameFavorites, FrameInfo, FrameShown, InstanceBar} from "./instance-bar";
import {machineAgents} from "./machine-tabs";
import {useHubFrame} from "./use-hub-frame";

// HubFrames is the open agent's chat beside the list (current), headed by
// the hub's bar until the chat draws its own, and the chats of the agents
// opened before it, which stay loaded out of sight (lib/hub-frames.ts), so
// going back to one shows it at once. Each frame is the same element for
// as long as its agent is kept, in the same place; a phone keeps fewer. Without an open agent
// the frames stay, out of sight, behind the overview or Issues.
export function HubFrames({
  current,
  instances,
  hubVersion,
  root,
  favorites,
  onBack,
  onHome,
  onOpen,
  onCancelUpdate,
  onManageFavorites,
}: {
  current: HubInstance | undefined;
  instances: ReadonlyMap<string, HubInstance>;
  hubVersion: string;
  root: string;
  favorites: Favorite[] | undefined;
  onBack: () => void;
  onHome: () => void;
  // Another agent, from a bar's switch.
  onOpen: (id: string) => void;
  onCancelUpdate: (id: string) => void;
  onManageFavorites: () => void;
}) {
  const phone = useMediaQuery("(max-width: 767.98px)");
  // The agents kept, most recently opened first, and the order of their
  // frames in the page, which never moves one: a frame moved loads again.
  const [frames, setFrames] = useState<{kept: readonly string[]; order: readonly string[]}>({kept: [], order: []});
  const next = keptFrames(frames.kept, current?.id, (id) => instances.has(id), phone ? KEPT_FRAMES_PHONE : KEPT_FRAMES);
  const order = frameOrder(frames.order, next);
  if (next !== frames.kept || order !== frames.order) setFrames({kept: next, order});
  const {register, readies} = useHubFrame({current: current?.id, kept: next, onBack, onHome, onCancelUpdate, onOpen, onManageFavorites});
  const all = useMemo(() => [...instances.values()], [instances]);

  return (
    <>
      {current && !readies.get(current.id) && (
        <InstanceBar instance={current} machine={machineAgents(all, current)} hubVersion={hubVersion} root={root} onBack={onBack} onHome={onHome} onOpen={onOpen} />
      )}
      {order.length > 0 && (
        <div className={cn(current ? "relative min-h-0 flex-1" : "pointer-events-none invisible absolute inset-0")} data-hub-frames>
          {order.map((id) => (
            <KeptFrame
              key={id}
              instance={instances.get(id)!}
              all={all}
              shown={id === current?.id}
              ready={readies.get(id) ?? 0}
              hubVersion={hubVersion}
              root={root}
              favorites={favorites}
              register={register}
            />
          ))}
        </div>
      )}
    </>
  );
}

// KeptFrame is one agent's chat in its frame, shown or out of sight: inert
// and hidden from screen readers then, so neither the keyboard nor a
// pointer reaches it. Once its chat is ready, it is told the instance, the
// favorites and whether it shows, whichever shows.
function KeptFrame({
  instance,
  all,
  shown,
  ready,
  hubVersion,
  root,
  favorites,
  register,
}: {
  instance: HubInstance;
  all: HubInstance[];
  shown: boolean;
  ready: number;
  hubVersion: string;
  root: string;
  favorites: Favorite[] | undefined;
  register: (instance: string, element: HTMLIFrameElement | null) => void;
}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const id = instance.id;
  const ref = useCallback(
    (element: HTMLIFrameElement | null) => {
      frame.current = element;
      register(id, element);
    },
    [id, register],
  );
  const machine = useMemo(() => machineAgents(all, instance), [all, instance]);
  return (
    <>
      {ready > 0 && (
        <>
          <FrameInfo frame={frame} ready={ready} instance={instance} machine={machine} hubVersion={hubVersion} root={root} />
          {favorites && <FrameFavorites frame={frame} ready={ready} favorites={favorites} />}
          <FrameShown frame={frame} ready={ready} shown={shown} />
        </>
      )}
      <iframe
        ref={ref}
        data-instance={id}
        title={`${displayName(instance)} chat`}
        src={chatURL(basePath, root, id, true)}
        inert={!shown}
        aria-hidden={shown ? undefined : true}
        tabIndex={shown ? undefined : -1}
        className={cn("absolute inset-0 size-full border-0 bg-background", ready > 0 && "pt-[env(safe-area-inset-top)]", !shown && "invisible")}
      />
    </>
  );
}
