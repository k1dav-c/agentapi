"use client";

import {useCallback, useEffect, useInsertionEffect, useRef, useState} from "react";
import {EventSourceConnection, initialConnectionState, type ConnectionState, type StreamListeners} from "@/lib/event-source";

// useEventSource keeps a server-sent event stream open (see
// EventSourceConnection) while url is set, and returns the connection's
// state and a way to open it again now. The listeners and callbacks may
// change from render to render: the latest are called.
export function useEventSource({
  url,
  withCredentials = false,
  staleAfter,
  listeners,
  onOpen,
  onError,
}: {
  url: string | null;
  withCredentials?: boolean;
  // The server's heartbeat interval, three times over; unset for a stream
  // without heartbeats.
  staleAfter?: number;
  // The listeners of one connection, made for each.
  listeners: () => StreamListeners;
  onOpen?: () => void;
  onError?: () => void;
}): ConnectionState & {reconnectNow: () => void} {
  const [state, setState] = useState(initialConnectionState);
  const latest = useRef({listeners, onOpen, onError});
  useInsertionEffect(() => {
    latest.current = {listeners, onOpen, onError};
  });
  const connection = useRef<EventSourceConnection | null>(null);

  useEffect(() => {
    if (!url) return;
    const current = new EventSourceConnection({
      url: () => url,
      withCredentials,
      staleAfter,
      listeners: () => latest.current.listeners(),
      onOpen: () => latest.current.onOpen?.(),
      onError: () => latest.current.onError?.(),
      onChange: setState,
    });
    connection.current = current;
    current.start();
    return () => {
      current.stop();
      connection.current = null;
      setState(initialConnectionState);
    };
  }, [url, withCredentials, staleAfter]);

  const reconnectNow = useCallback(() => connection.current?.reconnectNow(), []);
  return {...state, reconnectNow};
}
