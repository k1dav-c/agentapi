import {describe, expect, test} from "bun:test";
import {isImeComposing, numberKeyIndex, type NumberKeyEvent} from "./keys";

describe("isImeComposing", () => {
  test("a composing key press, as React and as a window listener see it", () => {
    expect(isImeComposing({keyCode: 13, nativeEvent: {isComposing: true}})).toBe(true);
    expect(isImeComposing({keyCode: 13, isComposing: true})).toBe(true);
  });

  test("Safari's committing Enter: isComposing false, keyCode 229", () => {
    expect(isImeComposing({keyCode: 229, nativeEvent: {isComposing: false}})).toBe(true);
    expect(isImeComposing({keyCode: 229, isComposing: false})).toBe(true);
  });

  test("a plain Enter", () => {
    expect(isImeComposing({keyCode: 13, nativeEvent: {isComposing: false}})).toBe(false);
    expect(isImeComposing({keyCode: 13})).toBe(false);
  });
});

// An element as numberKeyIndex sees it; inside lists the selectors of the
// ancestors it sits in.
function element(tagName: string, {inside = [] as string[], editable = false} = {}) {
  return {
    tagName,
    isContentEditable: editable,
    closest: (selector: string) => (selector.split(",").some((part) => inside.includes(part)) ? {} : null),
  };
}

const press = (key: string, overrides: Partial<NumberKeyEvent> = {}): NumberKeyEvent => ({
  key,
  keyCode: key.charCodeAt(0),
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  repeat: false,
  isComposing: false,
  defaultPrevented: false,
  target: element("BODY"),
  ...overrides,
});

describe("numberKeyIndex", () => {
  test("a digit picks an option", () => {
    expect(numberKeyIndex(press("1"), 3)).toBe(0);
    expect(numberKeyIndex(press("3"), 3)).toBe(2);
    expect(numberKeyIndex(press("1", {target: element("BUTTON")}), 3)).toBe(0);
  });

  test("only digits for options that exist", () => {
    expect(numberKeyIndex(press("4"), 3)).toBeNull();
    expect(numberKeyIndex(press("0"), 3)).toBeNull();
    expect(numberKeyIndex(press("a"), 3)).toBeNull();
    expect(numberKeyIndex(press("12"), 20)).toBeNull();
  });

  test("not with a modifier, held down, while composing or once handled", () => {
    expect(numberKeyIndex(press("1", {metaKey: true}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {ctrlKey: true}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {altKey: true}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {repeat: true}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {isComposing: true}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {keyCode: 229}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {defaultPrevented: true}), 3)).toBeNull();
  });

  test("not while typing, in the terminal or in a dialog or menu", () => {
    expect(numberKeyIndex(press("1", {target: element("INPUT")}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {target: element("TEXTAREA")}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {target: element("DIV", {editable: true})}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {target: element("TEXTAREA", {inside: [".xterm"]})}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {target: element("BUTTON", {inside: ['[role="dialog"]']})}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {target: element("DIV", {inside: ['[role="menu"]']})}), 3)).toBeNull();
    expect(numberKeyIndex(press("1", {target: element("DIV", {inside: ['[role="listbox"]']})}), 3)).toBeNull();
  });

  test("a target that isn't an element", () => {
    expect(numberKeyIndex(press("2", {target: null}), 3)).toBe(1);
    expect(numberKeyIndex(press("2", {target: {}}), 3)).toBe(1);
  });
});
