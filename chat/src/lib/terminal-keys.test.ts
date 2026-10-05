import {describe, expect, test} from "bun:test";
import {IOS_FORM_BAR_HEIGHT, isIOS, keyboardInset, terminalShortcuts} from "./terminal-keys";

describe("terminalShortcuts", () => {
  test("sends the escape sequences a terminal sends", () => {
    const value = (label: string) => terminalShortcuts.find((key) => key.label === label)?.value;
    expect(value("Arrow left")).toBe("\x1b[D");
    expect(value("Arrow right")).toBe("\x1b[C");
    expect(value("Shift+Tab")).toBe("\x1b[Z");
  });
  test("arrows and Enter come first, the keys that end or suspend the agent last", () => {
    expect(terminalShortcuts.slice(0, 3).map((key) => key.label)).toEqual(["Arrow up", "Arrow down", "Enter"]);
    expect(terminalShortcuts.slice(-2).map((key) => key.label)).toEqual(["Ctrl+D", "Ctrl+Z"]);
  });
  test("only keys that can end or suspend the agent need a second press", () => {
    expect(terminalShortcuts.filter((key) => key.risky).map((key) => key.label)).toEqual(["Ctrl+D", "Ctrl+Z"]);
  });
});

describe("keyboardInset", () => {
  test("iOS: the keyboard covers the bottom of the layout viewport", () => {
    expect(keyboardInset(844, {height: 508, offsetTop: 0})).toBe(336);
  });
  test("a scrolled visual viewport still counts only what is covered", () => {
    expect(keyboardInset(844, {height: 508, offsetTop: 120})).toBe(216);
  });
  test("iOS: the arrows-and-Done bar above the keyboard is covered too", () => {
    expect(keyboardInset(844, {height: 508, offsetTop: 0}, IOS_FORM_BAR_HEIGHT)).toBe(336 + 44);
    // Without a keyboard there is no bar either.
    expect(keyboardInset(844, {height: 844, offsetTop: 0}, IOS_FORM_BAR_HEIGHT)).toBe(0);
  });
  test("no keyboard, a layout that already shrank, or no visualViewport", () => {
    expect(keyboardInset(844, {height: 844, offsetTop: 0})).toBe(0);
    expect(keyboardInset(508, {height: 508, offsetTop: 0})).toBe(0);
    expect(keyboardInset(844, {height: 790, offsetTop: 0})).toBe(0);
    expect(keyboardInset(844, null)).toBe(0);
  });
});

describe("isIOS", () => {
  test("iPhone and iPad, including iPadOS reporting a Mac", () => {
    expect(isIOS("Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15", "iPhone", 5)).toBe(true);
    expect(isIOS("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15", "MacIntel", 5)).toBe(true);
  });
  test("not a Mac without touch, or Android", () => {
    expect(isIOS("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", "MacIntel", 0)).toBe(false);
    expect(isIOS("Mozilla/5.0 (Linux; Android 15; Pixel 9)", "Linux armv8l", 5)).toBe(false);
  });
});
