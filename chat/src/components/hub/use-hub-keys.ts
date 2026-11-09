"use client";

import {useEffect, useRef, type RefObject} from "react";
import {hubKey} from "@/lib/hub-keys";

// useHubKeys handles the hub page's keys (lib/hub-keys.ts). The listener
// stays the same from render to render: it reads the latest query and
// handlers.
export function useHubKeys({
  search,
  query,
  onClearSearch,
  onOverview,
  onIssues,
  onPin,
}: {
  search: RefObject<HTMLInputElement | null>;
  query: string;
  onClearSearch: () => void;
  onOverview: () => void;
  onIssues: () => void;
  // Pins or unpins the row's instance.
  onPin: (id: string) => void;
}) {
  const latest = useRef({query, onClearSearch, onOverview, onIssues, onPin});
  useEffect(() => {
    latest.current = {query, onClearSearch, onOverview, onIssues, onPin};
  });
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const active = document.activeElement as HTMLElement | null;
      const typing = target?.closest("input, textarea, [contenteditable=true]");
      const action = hubKey(
        event,
        {
          typing: !!typing,
          inSearch: !!typing && typing === search.current,
          inMenuOrDialog: !!target?.closest("[role=menu], [role=dialog], [role=alertdialog]"),
          menuOrDialogOpen: !!document.querySelector("[role=dialog], [role=menu]"),
          inList: !!active?.closest("[data-hub-list]"),
          onBody: !active || active === document.body,
          // A machine card's tab stands for its agent.
          row: active?.dataset.hubRow ?? active?.dataset.hubTab,
        },
        latest.current.query,
      );
      if (!action) return;
      switch (action.type) {
        case "search":
          event.preventDefault();
          search.current?.focus();
          return;
        case "clear-search":
          latest.current.onClearSearch();
          return;
        case "overview":
          latest.current.onOverview();
          return;
        case "issues":
          event.preventDefault();
          latest.current.onIssues();
          return;
        case "pin":
          event.preventDefault();
          latest.current.onPin(action.id);
          return;
        case "step": {
          const rows = [...document.querySelectorAll<HTMLButtonElement>("[data-hub-row]")];
          if (rows.length === 0) return;
          event.preventDefault();
          // From a machine card's tab, the steps start at its card's row.
          const from = active?.dataset.hubTab ? active.closest("[data-hub-card]")?.querySelector<HTMLButtonElement>("[data-hub-row]") : active;
          const at = rows.indexOf(from as HTMLButtonElement);
          const next = rows[at === -1 ? (action.step > 0 ? 0 : rows.length - 1) : Math.min(rows.length - 1, Math.max(0, at + action.step))];
          next.focus();
          next.scrollIntoView({block: "nearest"});
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [search]);
}
