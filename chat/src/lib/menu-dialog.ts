import {useState} from "react";

// A dialog opened from a dropdown menu item has to wait for the menu to
// close. Opened while the menu closes, the dialog takes the menu's
// "pointer-events: none" on <body> for the page's own style and puts it
// back when it closes, which leaves the whole page unclickable.
//
// dialogFromMenu returns the item's onSelect and the menu content's
// onCloseAutoFocus, which the menu calls once it has closed and let go of
// <body>.
export function dialogFromMenu(open: () => void) {
  let selected = false;
  return {
    onSelect: () => {
      selected = true;
    },
    onCloseAutoFocus: (event: Event) => {
      if (!selected) return;
      selected = false;
      // The dialog takes the focus, not the menu's trigger.
      event.preventDefault();
      open();
    },
  };
}

// useDialogFromMenu is dialogFromMenu for a component. open is the first
// render's: it must not depend on what the component renders.
export function useDialogFromMenu(open: () => void) {
  const [handlers] = useState(() => dialogFromMenu(open));
  return handlers;
}
