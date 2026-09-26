import {describe, expect, test} from "bun:test";
import {terminalOptionKeystrokes} from "./terminal-option";

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
