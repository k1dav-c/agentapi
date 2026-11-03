import {describe, expect, test} from "bun:test";
import {hubKey, type HubFocus} from "./hub-keys";

const key = (k: string, mods: Partial<{metaKey: boolean; ctrlKey: boolean; altKey: boolean}> = {}) => ({key: k, metaKey: false, ctrlKey: false, altKey: false, ...mods});
const none: HubFocus = {typing: false, inSearch: false, inMenuOrDialog: false, menuOrDialogOpen: false, inList: false, onBody: true};
const onRow: HubFocus = {...none, onBody: false, inList: true, row: "a"};
const elsewhere: HubFocus = {...none, onBody: false};

describe("hubKey", () => {
  test("the list's keys, with the focus in the list or on nothing", () => {
    expect(hubKey(key("j"), onRow, "")).toEqual({type: "step", step: 1});
    expect(hubKey(key("ArrowUp"), onRow, "")).toEqual({type: "step", step: -1});
    expect(hubKey(key("p"), onRow, "")).toEqual({type: "pin", id: "a"});
    expect(hubKey(key("ArrowDown"), none, "")).toEqual({type: "step", step: 1});
    expect(hubKey(key("p"), none, "")).toBeUndefined();
  });

  test("elsewhere the arrows, j, k and p are the page's", () => {
    // The overview, a filter chip, the header's buttons.
    for (const k of ["ArrowDown", "ArrowUp", "j", "k", "p"]) expect(hubKey(key(k), elsewhere, "")).toBeUndefined();
    expect(hubKey(key("ArrowDown"), {...onRow, inMenuOrDialog: true}, "")).toBeUndefined();
    expect(hubKey(key("j"), {...onRow, typing: true}, "")).toBeUndefined();
    expect(hubKey(key("ArrowDown", {altKey: true}), onRow, "")).toBeUndefined();
  });

  test("/ searches and Esc goes back from anywhere but a field", () => {
    expect(hubKey(key("/"), elsewhere, "")).toEqual({type: "search"});
    expect(hubKey(key("/"), {...elsewhere, typing: true}, "")).toBeUndefined();
    expect(hubKey(key("Escape"), elsewhere, "")).toEqual({type: "overview"});
    expect(hubKey(key("Escape"), {...elsewhere, typing: true, inSearch: true}, "build")).toEqual({type: "clear-search"});
    expect(hubKey(key("Escape"), {...elsewhere, menuOrDialogOpen: true}, "")).toBeUndefined();
  });
});
