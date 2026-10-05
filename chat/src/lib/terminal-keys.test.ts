import {describe, expect, test} from "bun:test";
import {keyboardInset, terminalShortcuts} from "./terminal-keys";

describe("terminalShortcuts", () => {
  test("sends the escape sequences a terminal sends", () => {
    const value = (label: string) => terminalShortcuts.find((key) => key.label === label)?.value;
    expect(value("Arrow left")).toBe("\x1b[D");
    expect(value("Arrow right")).toBe("\x1b[C");
    expect(value("Shift+Tab")).toBe("\x1b[Z");
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
  test("no keyboard, a layout that already shrank, or no visualViewport", () => {
    expect(keyboardInset(844, {height: 844, offsetTop: 0})).toBe(0);
    expect(keyboardInset(508, {height: 508, offsetTop: 0})).toBe(0);
    expect(keyboardInset(844, {height: 790, offsetTop: 0})).toBe(0);
    expect(keyboardInset(844, null)).toBe(0);
  });
});
