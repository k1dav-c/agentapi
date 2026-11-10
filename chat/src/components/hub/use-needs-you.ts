"use client";

import {useEffect, useRef, useState} from "react";
import {displayName, instanceState, type HubInstance, type InstanceState} from "@/lib/hub";
import {activity} from "@/lib/hub-format";
import {awayFromPage, playChime} from "@/lib/alert-sound";

// useNeedsYou tells people who aren't looking that an agent needs them: it
// returns what a screen reader announces, and with notify, shows a
// notification while the tab is hidden; a click on it opens the agent. With
// the sound on (alert-sound), it chimes once while the page is away.
export function useNeedsYou(instances: HubInstance[], notify: boolean, open: (id: string) => void): string {
  const [announcement, setAnnouncement] = useState("");
  const previousStates = useRef<Map<string, InstanceState>>(new Map());
  useEffect(() => {
    const before = previousStates.current;
    const next = new Map<string, InstanceState>();
    let chime = false;
    for (const instance of instances) {
      const state = instanceState(instance);
      next.set(instance.id, state);
      if (state === "needs-you" && before.size > 0 && before.get(instance.id) !== "needs-you") {
        const text = `${displayName(instance)}: ${activity(instance).text}`;
        setAnnouncement(`${displayName(instance)} needs you. ${activity(instance).text}`);
        chime = true;
        if (notify && document.hidden && typeof Notification !== "undefined" && Notification.permission === "granted") {
          const notification = new Notification(`${displayName(instance)} needs you`, {body: text, tag: `agentapi-hub-${instance.id}`});
          notification.onclick = () => {
            window.focus();
            open(instance.id);
            notification.close();
          };
        }
      }
    }
    previousStates.current = next;
    if (chime && awayFromPage()) playChime("needs-you");
  }, [instances, notify, open]);
  return announcement;
}
