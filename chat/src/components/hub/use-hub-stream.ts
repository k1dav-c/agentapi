"use client";

import {useEffect, useState} from "react";
import {applyHubEvent, hubEventNames, parseHubEvent, type DispatchBoard, type HubEventName, type HubInstance} from "@/lib/hub";
import type {Favorite} from "@/lib/hub-favorites";
import {checkHubSession, type HubSession} from "@/lib/hub-session";
import {getReconnectDelay} from "@/lib/reconnect";
import {useEventSource} from "../use-event-source";

// The hub sends a heartbeat every 15 s.
const STALE_STREAM_MS = 45_000;

// useHubStream keeps the hub page signed in and in step with the hub:
// whether this browser is signed in (undefined until the hub says), the
// instances and their changes from one stream kept open, the hub's own
// version, which the older instances are marked against, and the
// favorites (undefined until the hub sends them).
export function useHubStream(root: string) {
  const [session, setSession] = useState<HubSession | undefined>(undefined);
  const [instances, setInstances] = useState<Map<string, HubInstance>>(new Map());
  const [hubVersion, setHubVersion] = useState("");
  const [favorites, setFavorites] = useState<Favorite[] | undefined>(undefined);
  // The issue dispatch's machines and jobs; undefined until the hub sends
  // them, and from a hub before 3.12, which doesn't.
  const [dispatch, setDispatch] = useState<DispatchBoard | undefined>(undefined);
  // The last session check after the stream failed found the hub
  // unreachable.
  const [unreachable, setUnreachable] = useState(false);

  // Signed in or not; a hub that doesn't answer is asked again, with a
  // backoff, and at once when the browser comes back online.
  const [sessionAttempt, setSessionAttempt] = useState(0);
  useEffect(() => {
    if (!root) return;
    let active = true;
    void checkHubSession(root).then((result) => {
      if (active) setSession(result);
    });
    return () => {
      active = false;
    };
  }, [root, sessionAttempt]);
  useEffect(() => {
    if (session !== "unreachable") return;
    const retry = () => setSessionAttempt((n) => n + 1);
    const timer = setTimeout(retry, getReconnectDelay(sessionAttempt + 1));
    window.addEventListener("online", retry);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("online", retry);
    };
  }, [session, sessionAttempt]);

  const stream = useEventSource({
    url: session === "signed-in" && root ? `${root}/api/events` : null,
    withCredentials: true,
    staleAfter: STALE_STREAM_MS,
    listeners: () => {
      // An event that isn't what its name says is left out.
      const on = (type: HubEventName) => (message: MessageEvent<string>) => {
        const event = parseHubEvent(type, message.data);
        if (!event) return;
        if (event.type === "favorites") {
          setFavorites(event.favorites);
          return;
        }
        if (event.type === "dispatch") {
          setDispatch(event.board);
          return;
        }
        if (event.type === "instances") setHubVersion(event.hubVersion);
        setInstances((previous) => applyHubEvent(previous, event));
      };
      return Object.fromEntries(hubEventNames.map((type) => [type, on(type)]));
    },
    onOpen: () => setUnreachable(false),
    // EventSource hides the status: a session that expired comes back as a
    // sign-in, a hub that doesn't answer as unreachable.
    onError: () => {
      void checkHubSession(root).then((result) => {
        if (result === "signed-out") setSession("signed-out");
        setUnreachable(result === "unreachable");
      });
    },
  });

  return {
    session,
    signedIn: () => setSession("signed-in"),
    // Asks the hub again now, as the user asked.
    retrySession: () => setSessionAttempt((n) => n + 1),
    instances,
    hubVersion,
    favorites,
    // The favorites as the hub answered a change, before its stream tells
    // of it.
    setFavorites,
    dispatch,
    unreachable,
    status: stream.status,
  };
}
