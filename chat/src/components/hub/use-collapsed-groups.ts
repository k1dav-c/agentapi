"use client";

import {useCallback} from "react";
import {useHubSetting} from "@/lib/hub-settings";

// useCollapsedGroups is the sections of the hub's list folded in this
// browser, by their keys (ListSection.key), with toggle, which folds one
// or unfolds it, and rename, which keeps a folded group folded under its
// new name.
export function useCollapsedGroups() {
  const [collapsed, setCollapsed] = useHubSetting("collapsed");
  const change = useCallback(
    (edit: (next: Set<string>) => void) =>
      setCollapsed((previous) => {
        const next = new Set(previous);
        edit(next);
        return next;
      }),
    [setCollapsed],
  );
  const toggle = useCallback(
    (key: string) =>
      change((next) => {
        if (!next.delete(key)) next.add(key);
      }),
    [change],
  );
  const rename = useCallback(
    (from: string, to: string) =>
      change((next) => {
        if (next.delete(from)) next.add(to);
      }),
    [change],
  );
  return {collapsed, toggle, rename};
}
