import {describe, expect, test} from "bun:test";
import {dialogFromMenu} from "./menu-dialog";

describe("dialogFromMenu", () => {
  test("opens the dialog once the menu has closed", () => {
    let opened = 0;
    const menu = dialogFromMenu(() => opened++);
    menu.onSelect();
    expect(opened).toBe(0);
    const event = new Event("focusout", {cancelable: true});
    menu.onCloseAutoFocus(event);
    expect(opened).toBe(1);
    // The dialog takes the focus, not the menu's trigger.
    expect(event.defaultPrevented).toBe(true);
  });

  test("leaves a menu closed without choosing the item alone", () => {
    let opened = 0;
    const menu = dialogFromMenu(() => opened++);
    menu.onSelect();
    menu.onCloseAutoFocus(new Event("focusout", {cancelable: true}));
    const event = new Event("focusout", {cancelable: true});
    menu.onCloseAutoFocus(event);
    expect(opened).toBe(1);
    expect(event.defaultPrevented).toBe(false);
  });
});
