import {describe, expect, test} from "bun:test";
import {terminalShortcuts, withModifier} from "./terminal-keys";

describe("terminalShortcuts", () => {
  test("sends the escape sequences a terminal sends", () => {
    const value = (label: string) => terminalShortcuts.find((key) => key.label === label)?.value;
    expect(value("Arrow left")).toBe("\x1b[D");
    expect(value("Arrow right")).toBe("\x1b[C");
    expect(value("Shift+Tab")).toBe("\x1b[Z");
    expect(value("Ctrl+R")).toBe("\x12");
    expect(value("Ctrl+W")).toBe("\x17");
    expect(value("Page up")).toBe("\x1b[5~");
    expect(value("Pipe")).toBe("|");
  });
  test("a held Ctrl or Alt changes the next key typed", () => {
    expect(withModifier("c", "ctrl")).toBe("\x03");
    expect(withModifier("R", "ctrl")).toBe("\x12");
    expect(withModifier("[", "ctrl")).toBe("\x1b");
    expect(withModifier(" ", "ctrl")).toBe("\x00");
    expect(withModifier("1", "ctrl")).toBe("1");
    expect(withModifier("b", "alt")).toBe("\x1bb");
    expect(withModifier("", "ctrl")).toBe("");
  });
  test("arrows and Enter come first, the keys that end or suspend the agent last", () => {
    expect(terminalShortcuts.slice(0, 3).map((key) => key.label)).toEqual(["Arrow up", "Arrow down", "Enter"]);
    expect(terminalShortcuts.slice(-2).map((key) => key.label)).toEqual(["Ctrl+D", "Ctrl+Z"]);
  });
  test("only keys that can end or suspend the agent need a second press", () => {
    expect(terminalShortcuts.filter((key) => key.risky).map((key) => key.label)).toEqual(["Ctrl+D", "Ctrl+Z"]);
  });
});
