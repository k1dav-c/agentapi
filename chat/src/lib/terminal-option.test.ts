import {describe, expect, test} from "bun:test";
import {parseTerminalOptions, terminalOptionKeystrokes} from "./terminal-option";

describe("terminalOptionKeystrokes", () => {
  test("Claude selects numbered options without Enter", () => {
    expect(terminalOptionKeystrokes("claude", "3")).toBe("3");
  });

  test("Codex selects numbered options without Enter", () => {
    expect(terminalOptionKeystrokes("codex", "2")).toBe("2");
  });

  test("other agents confirm numbered options with Enter", () => {
    expect(terminalOptionKeystrokes("aider", "2")).toBe("2\r");
  });

  test("Enter-only and arrow options are not doubled", () => {
    expect(terminalOptionKeystrokes("claude", "\r")).toBe("\r");
    expect(terminalOptionKeystrokes("codex", "\r")).toBe("\r");
    expect(terminalOptionKeystrokes("codex", "\x1b[B\r")).toBe("\x1b[B\r");
  });
});

describe("parseTerminalOptions", () => {
  test("ignores a numbered list in the answer above the dialog", () => {
    const prompt = [
      "● Plan:",
      "  1. Refactor the parser",
      "  2. Add tests",
      "  3. Update docs",
      "",
      " Do you want to create c.txt?",
      " ❯ 1. Yes",
      "   2. Yes, and switch to accept edits (auto-approve file edits and common file",
      "      commands) for this session (shift+tab)",
      "   3. No",
      " Esc to cancel · Tab to amend",
    ].join("\n");
    expect(parseTerminalOptions(prompt)).toEqual([
      {key: "1", label: "Yes"},
      {key: "2", label: "Yes, and switch to accept edits (auto-approve file edits and common file"},
      {key: "3", label: "No"},
    ]);
  });

  test("Codex options", () => {
    const prompt = [
      "  Update available · 0.158.0 → 0.160.0",
      "› 1. Update now",
      "  2. Skip",
      "  3. Skip until next version",
      "",
      "  enter continue · esc skip",
    ].join("\n");
    expect(parseTerminalOptions(prompt).map((o) => o.label)).toEqual(["Update now", "Skip", "Skip until next version"]);
  });

  test("unnumbered options move the cursor relative to the highlighted one", () => {
    const prompt = [
      " Claude Code'll be able to read, edit, and execute files here.",
      "",
      " ❯ No, exit",
      "   Yes, I trust this folder",
      "",
      " Enter to confirm · Esc to cancel",
    ].join("\n");
    expect(parseTerminalOptions(prompt)).toEqual([
      {key: "\r", label: "No, exit"},
      {key: "\x1b[B\r", label: "Yes, I trust this folder"},
    ]);
  });

  test("plain text has no options", () => {
    expect(parseTerminalOptions("just text")).toEqual([]);
  });
});
