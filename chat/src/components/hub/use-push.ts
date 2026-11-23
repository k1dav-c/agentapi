"use client";

import {useEffect, useMemo, useState} from "react";
import {toast} from "sonner";
import {HubAPIError} from "@/lib/hub-api";
import {
  browserPush,
  browserPushEnvironment,
  createHubPush,
  deviceLabel,
  parseOpenMessage,
  PushPermissionError,
  pushSupport,
  setAppBadge,
  type HubPush,
  type OpenTarget,
  type PushSupport,
} from "@/lib/hub-push";
import {useHubSetting} from "@/lib/hub-settings";
import {basePath} from "./instance-bar";

export interface HubPushState {
  // This browser gets the hub's pushes.
  push: boolean;
  support: PushSupport;
  api: HubPush;
  togglePush: () => Promise<void>;
  testPush: () => Promise<void>;
  // signOut stops pushing to this browser before the page signs out.
  signOut: () => Promise<void>;
}

// usePush is whether the hub pushes to this browser (the browser keeps
// it), and the menu's actions: turn it on or off, send a test. What a
// device gets is kept on the hub (the settings' Devices). While it is on,
// the page tells the hub its subscription again once signed in, which
// keeps the device for that session; a click on a notification of a page
// already open opens the agent or the issue here (open). needYou is how
// many agents need you, for the app icon's badge.
export function usePush(root: string, signedIn: boolean, open: (target: OpenTarget) => void, needYou: number): HubPushState {
  const [push, setPush] = useHubSetting("push");
  // Known after the first render: the page is rendered ahead without a
  // browser.
  const [support, setSupport] = useState<PushSupport>("unsupported");
  const [label, setLabel] = useState("");
  useEffect(() => {
    const env = browserPushEnvironment();
    setSupport(pushSupport(env));
    setLabel(deviceLabel(navigator.userAgent, env.standalone, navigator.maxTouchPoints));
  }, []);
  const api = useMemo(() => createHubPush(root, browserPush(basePath), label), [root, label]);

  useEffect(() => {
    if (!push || !signedIn || support !== "supported" || !label) return;
    api.sync().catch(() => {
      // The menu's Send a test says what is wrong.
    });
  }, [push, signedIn, support, label, api]);

  useEffect(() => {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
    const onMessage = (event: MessageEvent) => {
      const target = parseOpenMessage(event.data);
      if (target) open(target);
    };
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => navigator.serviceWorker.removeEventListener("message", onMessage);
  }, [open]);

  // The badge says what the page does while it is open; the pushes, while
  // it is closed.
  useEffect(() => {
    if (signedIn) setAppBadge(needYou);
  }, [signedIn, needYou]);

  const togglePush = async () => {
    if (push) {
      // Off once the hub forgot it: what reads the devices then sees it gone.
      await api.disable().catch((error: unknown) => toast.error("Couldn't turn push off on the hub", {description: messageOf(error)}));
      setPush(false);
      return;
    }
    switch (support) {
      case "insecure":
        toast.error("Push needs a secure (https) page", {description: "Open the hub over https, such as through your Coder workspace or a reverse proxy."});
        return;
      case "home-screen":
        toast("Add the hub to your Home Screen first", {description: "On an iPhone or iPad only an app on the Home Screen gets pushes: Share → Add to Home Screen, then open the hub from there."});
        return;
      case "unsupported":
        toast.error("This browser can't get pushes");
        return;
      case "supported":
    }
    try {
      await api.enable();
      setPush(true);
      toast.success("Push is on for this device", {description: "Send a test from this menu to check that it arrives; Push settings choose what it gets."});
    } catch (error) {
      if (error instanceof PushPermissionError) {
        toast.error("Notifications are blocked for this site", {description: "Allow them in the browser's site settings, then try again."});
        return;
      }
      toast.error("Couldn't turn push on", {description: messageOf(error)});
    }
  };

  const testPush = async () => {
    try {
      await api.test();
      toast.success("The hub sent a test notification");
    } catch (error) {
      // The hub forgot this browser (its push service ended the
      // subscription, or its session did): subscribe again.
      if (error instanceof HubAPIError && (error.status === 404 || error.status === 410)) {
        try {
          await api.enable();
          await api.test();
          toast.success("The hub sent a test notification");
          return;
        } catch (again) {
          error = again;
        }
      }
      toast.error("Couldn't push to this device", {description: messageOf(error)});
    }
  };

  const signOut = async () => {
    setAppBadge(0);
    if (!push) return;
    await api.disable().catch(() => {
      // The hub forgets it when the session ends anyway.
    });
    setPush(false);
  };

  return {push, support, api, togglePush, testPush, signOut};
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
