"use client";

import {useLayoutEffect, useMemo, useRef} from "react";
import type {ListSection} from "@/lib/hub-order";

// useRowFocusRestore keeps the focus on the list's row that has it, by
// instance, or on its menu's button. React moves rows in the page when
// their order changes, and a row that moves loses the focus: it gets it
// back, unless something else took it. The list's element takes the
// handlers it returns.
export function useRowFocusRestore(sections: readonly ListSection[]) {
  const focusedRow = useRef<{id: string; menu: boolean} | undefined>(undefined);
  const rowOrder = sections.map((section) => section.instances.map((instance) => instance.id).join(" ")).join("\n");
  useLayoutEffect(() => {
    const focused = focusedRow.current;
    if (!focused || (document.activeElement && document.activeElement !== document.body)) return;
    const attribute = focused.menu ? "data-hub-row-menu" : "data-hub-row";
    document.querySelector<HTMLElement>(`[${attribute}="${CSS.escape(focused.id)}"]`)?.focus({preventScroll: true});
  }, [rowOrder]);
  return useMemo(
    () => ({
      onFocus: (event: React.FocusEvent<HTMLElement>) => {
        const {hubRow, hubRowMenu} = (event.target as HTMLElement).dataset;
        focusedRow.current = hubRow ? {id: hubRow, menu: false} : hubRowMenu ? {id: hubRowMenu, menu: true} : undefined;
      },
      onBlur: (event: React.FocusEvent<HTMLElement>) => {
        // Not when the row left the page with the focus (relatedTarget is
        // none then): it gets it back.
        if (event.relatedTarget) focusedRow.current = undefined;
      },
    }),
    [],
  );
}
