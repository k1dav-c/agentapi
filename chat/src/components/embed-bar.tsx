"use client";

import {useSearchParams} from "next/navigation";
import {EmbedStatusBar} from "./embed-status-bar";

// EmbedBar is the chat's status bar, unless the hub shows the chat under a
// bar of its own (hub=1).
export function EmbedBar() {
  const searchParams = useSearchParams();
  if (searchParams.get("hub") === "1") return null;
  return <EmbedStatusBar />;
}
