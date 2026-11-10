"use client";

import {toast} from "sonner";
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
    if (typeof Notification === "undefined") {
      toast.error("This browser can't show notifications here", {description: "They need a secure (https) page; the sound works without."});
      return;
    }
    const permission = Notification.permission === "default" ? await Notification.requestPermission() : Notification.permission;
    if (permission !== "granted") {
      toast.error("Notifications are blocked for this site", {description: "Allow them in the browser's site settings, then try again."});
      return;
    }
    setNotify(true);
  };
  return {notify, toggleNotify};
}
