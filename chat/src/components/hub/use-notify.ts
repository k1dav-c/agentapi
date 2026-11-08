"use client";

import {useHubSetting} from "@/lib/hub-settings";

// useNotify is whether the hub page notifies this browser of an agent that
// needs you (the browser keeps it), and toggleNotify, which turns it on
// once the browser allows notifications, or off.
export function useNotify(): {notify: boolean; toggleNotify: () => Promise<void>} {
  const [notify, setNotify] = useHubSetting("notify");
  const toggleNotify = async () => {
    if (notify) {
      setNotify(false);
      return;
    }
    if (typeof Notification === "undefined") return;
    const permission = Notification.permission === "default" ? await Notification.requestPermission() : Notification.permission;
    setNotify(permission === "granted");
  };
  return {notify, toggleNotify};
}
