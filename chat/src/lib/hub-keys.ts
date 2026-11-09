// The hub page's keys: / searches, i opens Issues, j and k (or the arrows)
// move between the rows, p pins or unpins the row with the focus, Esc
// clears the search or goes back to the overview. The rows' keys are the list's: they work
// with the focus in it (data-hub-list) or on nothing, not in the overview,
// the header's buttons, a menu or a dialog, which take the arrows
// themselves.

export type HubKey =
  | {type: "search"}
  | {type: "clear-search"}
  | {type: "overview"}
  | {type: "issues"}
  | {type: "pin"; id: string}
  | {type: "step"; step: 1 | -1};

// Where the focus is when a key goes down.
export interface HubFocus {
  // In a field: the search, or another.
  typing: boolean;
  inSearch: boolean;
  // In a menu or a dialog, or one is open.
  inMenuOrDialog: boolean;
  menuOrDialogOpen: boolean;
  // In the list, or on nothing (the page's body).
  inList: boolean;
  onBody: boolean;
  // The row the focus is on (its data-hub-row).
  row?: string;
}

export function hubKey(
  event: {key: string; metaKey: boolean; ctrlKey: boolean; altKey: boolean},
  focus: HubFocus,
  query: string,
): HubKey | undefined {
  if (event.key === "/" && !focus.typing) return {type: "search"};
  if (event.key === "Escape") {
    if (focus.inSearch && query) return {type: "clear-search"};
    return focus.menuOrDialogOpen ? undefined : {type: "overview"};
  }
  if (focus.typing || focus.inMenuOrDialog || event.metaKey || event.ctrlKey || event.altKey) return undefined;
  // From anywhere but a field, a menu or a dialog.
  if (event.key === "i") return focus.menuOrDialogOpen ? undefined : {type: "issues"};
  if (!focus.inList && !focus.onBody) return undefined;
  if (event.key === "p") return focus.row ? {type: "pin", id: focus.row} : undefined;
  if (event.key === "j" || event.key === "ArrowDown") return {type: "step", step: 1};
  if (event.key === "k" || event.key === "ArrowUp") return {type: "step", step: -1};
  return undefined;
}
