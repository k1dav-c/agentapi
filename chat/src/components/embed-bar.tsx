"use client";

import {useSearchParams} from "next/navigation";
import {EmbedStatusBar} from "./embed-status-bar";
import {FrameBar} from "./hub/frame-bar";

// EmbedBar is the chat's status bar, or the hub's bar for the instance when
// the hub opens the chat (hub=1).
export function EmbedBar() {
  const searchParams = useSearchParams();
  if (searchParams.get("hub") === "1") return <FrameBar />;
  return <EmbedStatusBar />;
}
