"use client";

import {useEffect, useMemo, useRef, useState} from "react";
import {toast} from "sonner";
import {HubAPIError} from "@/lib/hub-api";
import {browserPush, browserPushEnvironment, createHubPush, parseOpenMessage, PushPermissionError, pushSupport, type PushSupport} from "@/lib/hub-push";
import {useHubSetting} from "@/lib/hub-settings";
import {basePath} from "./instance-bar";

export interface HubPushState {
  // This browser gets the hub's pushes.
  push: boolean;
  // ...of the finished tasks too.
  pushFinished: boolean;
  support: PushSupport;
  togglePush: () => Promise<void>;
  togglePushFinished: () => Promise<void>;
  testPush: () => Promise<void>;
}

// usePush is whether the hub pushes to this browser (the browser keeps
// it), and the menu's actions: turn it on or off, push the finished tasks
// too, send a test. While it is on, the page tells the hub its
// subscription again once signed in; a click on a notification of a page
// already open opens the agent here (open).
export function usePush(root: string, signedIn: boolean, open: (id: string) => void): HubPushState {
  const [push, setPush] = useHubSetting("push");
  const [pushFinished, setPushFinished] = useHubSetting("pushFinished");
  // Known after the first render: the page is rendered ahead without a
  // browser.
  const [support, setSupport] = useState<PushSupport>("unsupported");
  useEffect(() => setSupport(pushSupport(browserPushEnvironment())), []);
  const api = useMemo(() => createHubPush(root, browserPush(basePath)), [root]);
  const finished = useRef(pushFinished);
  finished.current = pushFinished;

  useEffect(() => {
    if (!push || !signedIn || support !== "supported") return;
    api.sync(finished.current).catch(() => {
      // The menu's Send a test says what is wrong.
    });
  }, [push, signedIn, support, api]);

  useEffect(() => {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
    const onMessage = (event: MessageEvent) => {
      const id = parseOpenMessage(event.data);
      if (id) open(id);
    };
    navigator.serviceWorker.addEventListener("message", onMessage);
    return () => navigator.serviceWorker.removeEventListener("message", onMessage);
  }, [open]);

  const togglePush = async () => {
    if (push) {
      setPush(false);
      await api.disable().catch((error: unknown) => toast.error("Couldn't turn push off on the hub", {description: messageOf(error)}));
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
      await api.enable(pushFinished);
      setPush(true);
      toast.success("Push is on for this device", {description: "Send a test from this menu to check that it arrives."});
    } catch (error) {
      if (error instanceof PushPermissionError) {
        toast.error("Notifications are blocked for this site", {description: "Allow them in the browser's site settings, then try again."});
        return;
      }
      toast.error("Couldn't turn push on", {description: messageOf(error)});
    }
  };

  const togglePushFinished = async () => {
    const next = !pushFinished;
    setPushFinished(next);
    if (push) await api.sync(next).catch((error: unknown) => toast.error("Couldn't tell the hub", {description: messageOf(error)}));
  };

  const testPush = async () => {
    try {
      await api.test();
      toast.success("The hub sent a test notification");
    } catch (error) {
      // The hub forgot this browser (its push service ended the
      // subscription): subscribe again.
      if (error instanceof HubAPIError && (error.status === 404 || error.status === 410)) {
        try {
          await api.enable(pushFinished);
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

  return {push, pushFinished, support, togglePush, togglePushFinished, testPush};
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
